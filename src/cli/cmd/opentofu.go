package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/opentofu"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/spf13/cobra"
)

var openTofuRunPlan bool

var openTofuCmd = &cobra.Command{
	Use:   "opentofu",
	Short: "Infrastructure convergence via the declared OpenTofu/Terraform roots",
}

var openTofuRunCmd = &cobra.Command{
	Use:   "run <root-id>",
	Short: "Plan and apply one declared root by id",
	Long: `Plan and apply a single root from the opentofu: library — the ONLY way a
converge: false runbook applies. CI never reaches runbooks; this verb is the
human lane, with the same binary (toolchains.want.<backend>), credentials and
saved-plan apply as the converge set.

Examples:
  stagefreight opentofu run oracle-site --plan
  stagefreight opentofu run oracle-site`,
	Args: cobra.ExactArgs(1),
	RunE: runOpenTofuRun,
}

func init() {
	openTofuRunCmd.Flags().BoolVar(&openTofuRunPlan, "plan", false, "plan only, no changes")
	openTofuCmd.AddCommand(openTofuRunCmd)
	rootCmd.AddCommand(openTofuCmd)
}

func runOpenTofuRun(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	root, ok := cfg.OpenTofu.RootByID(args[0])
	if !ok {
		return fmt.Errorf("root %q is not declared in opentofu.roots", args[0])
	}
	// The human lane wants a loud failure, not a graceful skip, when credentials are
	// absent — unlike CI, where a protected variable legitimately withholds them.
	if !cfg.OpenTofu.CredentialsPresent() {
		return fmt.Errorf("no %s_* variables set — export the credentials to run roots", cfg.OpenTofu.EnvPrefix())
	}

	backend, err := newOpenTofuBackend(cfg)
	if err != nil {
		return err
	}
	backend.SelectRoot(root)
	backend.SetStream(openTofuLiveStream)

	ciCtx := ci.ResolveContext()
	rctx := &runtime.RuntimeContext{
		CI:       ciCtx,
		Invoker:  runtime.DetectInvoker(ciCtx),
		RepoRoot: rootDir,
		DryRun:   openTofuRunPlan,
	}
	start := time.Now()
	runErr := runtime.RunLifecycleWith(cmd.Context(), cfg, rctx, opentofu.Mode, backend)
	renderOpenTofuRun(os.Stdout, cfg, backend, time.Since(start))
	if runErr != nil {
		return runErr
	}
	return openTofuFailure(cfg, backend.RootResults())
}

// newOpenTofuBackend resolves opentofu.backend from the lifecycle registry.
func newOpenTofuBackend(appCfg *config.Config) (*opentofu.Backend, error) {
	lb, err := runtime.ResolveBackend(opentofu.Mode, appCfg.OpenTofu.Backend)
	if err != nil {
		return nil, fmt.Errorf("opentofu.backend: %w", err)
	}
	b, ok := lb.(*opentofu.Backend)
	if !ok {
		return nil, fmt.Errorf("opentofu.backend %q is not an opentofu backend", appCfg.OpenTofu.Backend)
	}
	return b, nil
}

// runOpenTofuConverge is the CI perform-phase entry: plans and applies the converge set,
// records the opentofu subsystem + facts into cistate, renders the results box, and
// fails loud on any failed required root. When the credentials are absent (protected CI
// variables on an unprotected ref), it renders a clear skip.
func runOpenTofuConverge(ctx context.Context, appCfg *config.Config, rootDir string, dryRun bool) error {
	start := time.Now()
	if !appCfg.OpenTofu.CredentialsPresent() {
		renderCISkip("OpenTofu", start, "credentials not available in this pipeline context")
		return nil
	}

	backend, err := newOpenTofuBackend(appCfg)
	if err != nil {
		return err
	}
	backend.SetStream(openTofuLiveStream)
	ciCtx := ci.ResolveContext()
	rctx := &runtime.RuntimeContext{
		CI:       ciCtx,
		Invoker:  runtime.DetectInvoker(ciCtx),
		RepoRoot: rootDir,
		DryRun:   dryRun,
	}
	runErr := runtime.RunLifecycleWith(ctx, appCfg, rctx, opentofu.Mode, backend)
	renderOpenTofuRun(os.Stdout, appCfg, backend, time.Since(start))

	// Record BEFORE the failure check so a failed converge still narrates.
	recordOpenTofuState(rootDir, appCfg, backend, runErr)
	if runErr != nil {
		return runErr
	}
	return openTofuFailure(appCfg, backend.RootResults())
}

// openTofuFailure is the subsystem's fail-loud contract: a failed REQUIRED root fails
// the phase. Roots declared required: false are advisory.
func openTofuFailure(appCfg *config.Config, results []opentofu.RootResult) error {
	requiredFailed := 0
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if root, ok := appCfg.OpenTofu.RootByID(r.ID); !ok || root.IsRequired() {
			requiredFailed++
		}
	}
	if requiredFailed > 0 {
		return fmt.Errorf("opentofu: %d/%d roots failed", requiredFailed, len(results))
	}
	return nil
}

// recordOpenTofuState persists the opentofu subsystem outcome + {opentofu.*} facts.
// Recording never fails the converge.
func recordOpenTofuState(rootDir string, appCfg *config.Config, backend *opentofu.Backend, runErr error) {
	results := backend.RootResults()
	var applied, failed int
	var total opentofu.Changes
	for _, r := range results {
		total.Add += r.Changes.Add
		total.Change += r.Changes.Change
		total.Destroy += r.Changes.Destroy
		if r.Applied {
			applied++
		}
		if r.Err != nil {
			failed++
		}
	}
	outcome, reason := "success", ""
	switch {
	case runErr != nil:
		outcome, reason = "failed", runErr.Error()
	case failed > 0:
		outcome, reason = "failed", fmt.Sprintf("%d of %d roots failed", failed, len(results))
	}
	required := runErr != nil || outcome != "failed" || openTofuFailure(appCfg, results) != nil
	facts := map[string]string{
		"total":   strconv.Itoa(len(results)),
		"applied": strconv.Itoa(applied),
		"add":     strconv.Itoa(total.Add),
		"change":  strconv.Itoa(total.Change),
		"destroy": strconv.Itoa(total.Destroy),
	}
	if failed > 0 {
		facts["failed"] = strconv.Itoa(failed)
	}
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "opentofu", Attempted: true, Completed: true, Required: required,
			Outcome: outcome, Reason: reason,
			Results: facts,
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}
}

// openTofuLiveStream streams a root's raw output into a collapsed CI log group as it
// runs; the results box afterwards stays the summary verdict.
func openTofuLiveStream(rootID string) io.WriteCloser {
	return output.OpenLogGroup(os.Stdout, "sf_opentofu_"+rootID, fmt.Sprintf("opentofu: %s (live)", rootID))
}

// renderOpenTofuRun renders the per-root results box: the binary's identity, then one
// row per root with its changes; failures tail their output.
func renderOpenTofuRun(w *os.File, appCfg *config.Config, backend *opentofu.Backend, elapsed time.Duration) {
	color := output.UseColor()
	sec := output.NewSection(w, "OpenTofu", elapsed, color)
	if id := backend.Identity(); id != "" {
		sec.Row("binary  %s", id)
	}

	results := backend.RootResults()
	if len(results) == 0 {
		sec.Row("no roots executed")
		sec.Close()
		return
	}
	for i, r := range results {
		status := "success"
		suffix := "  " + r.Summary()
		if r.Err != nil {
			status = "failed"
			if root, ok := appCfg.OpenTofu.RootByID(r.ID); ok && !root.IsRequired() {
				status = "warning"
				suffix += " (advisory — required: false)"
			}
		}
		output.RowStatus(sec, fmt.Sprintf("[%d/%d] %s", i+1, len(results), r.ID), suffix, status, color)
		if r.Err != nil && r.Output != "" {
			tail := r.Output
			if len(tail) > 1200 {
				tail = tail[len(tail)-1200:]
			}
			for _, line := range strings.Split(strings.TrimSpace(tail), "\n") {
				fmt.Fprintf(w, "    │   %s\n", line)
			}
		}
	}
	sec.Close()
}
