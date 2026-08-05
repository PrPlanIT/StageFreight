package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ansible"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/spf13/cobra"
)

var (
	ansibleRunPlan      bool
	ansibleRunExtraVars []string
)

var ansibleCmd = &cobra.Command{
	Use:   "ansible",
	Short: "Host convergence via the declared playbook library",
}

var ansibleRunCmd = &cobra.Command{
	Use:   "run <playbook-id>",
	Short: "Run one declared playbook by id",
	Long: `Run a single playbook from the ansible: library — the ONLY way a
converge: false runbook executes. CI never reaches runbooks; this verb is the
human lane, with the same execution image, strict host-key posture, and
--check plan preview as the converge set.

Examples:
  stagefreight ansible run provision-hosts --plan
  stagefreight ansible run postgres-major-upgrade -e target_version=17`,
	Args: cobra.ExactArgs(1),
	RunE: runAnsibleRun,
}

func init() {
	ansibleRunCmd.Flags().BoolVar(&ansibleRunPlan, "plan", false, "ansible-playbook --check --diff preview, no changes")
	ansibleRunCmd.Flags().StringArrayVarP(&ansibleRunExtraVars, "extra-vars", "e", nil, "launch-time vars passed to ansible-playbook -e (repeatable)")
	ansibleCmd.AddCommand(ansibleRunCmd)
	rootCmd.AddCommand(ansibleCmd)
}

func runAnsibleRun(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	play, ok := cfg.Ansible.PlaybookByID(args[0])
	if !ok {
		return fmt.Errorf("playbook %q is not declared in ansible.playbooks", args[0])
	}
	// The human lane wants a loud failure, not a graceful skip, when the key
	// is absent — unlike CI, where a protected variable legitimately withholds it.
	if !ansible.CredentialsPresent(cfg) {
		return fmt.Errorf("%s_SSH_KEY not set — export the node SSH key to run playbooks", cfg.Ansible.SSH.EnvPrefix())
	}

	backend := &ansible.Backend{}
	backend.SelectPlay(play)
	backend.SetExtraVars(ansibleRunExtraVars)

	ciCtx := ci.ResolveContext()
	rctx := &runtime.RuntimeContext{
		CI:       ciCtx,
		Invoker:  runtime.DetectInvoker(ciCtx),
		RepoRoot: rootDir,
		DryRun:   ansibleRunPlan,
	}
	start := time.Now()
	runErr := runtime.RunLifecycleWith(cmd.Context(), cfg, rctx, "ansible", backend)
	renderAnsibleRun(os.Stdout, backend, rctx, time.Since(start))
	if runErr != nil {
		return runErr
	}
	return ansibleFailure(cfg, rctx.Result())
}

// runAnsibleConverge is the CI perform-phase entry: runs the converge set,
// records the ansible subsystem + facts into cistate, renders the results box,
// and fails loud on any failed or unreachable host. When the SSH key is absent
// (a protected CI variable on an unprotected ref), it renders a clear skip —
// the signature of a correctly-protected setup, not an error.
func runAnsibleConverge(ctx context.Context, appCfg *config.Config, rootDir string, dryRun bool) error {
	start := time.Now()
	if !ansible.CredentialsPresent(appCfg) {
		renderCISkip("Ansible", start, "credentials not available in this pipeline context")
		return nil
	}

	backend := &ansible.Backend{}
	ciCtx := ci.ResolveContext()
	rctx := &runtime.RuntimeContext{
		CI:       ciCtx,
		Invoker:  runtime.DetectInvoker(ciCtx),
		RepoRoot: rootDir,
		DryRun:   dryRun,
	}
	runErr := runtime.RunLifecycleWith(ctx, appCfg, rctx, "ansible", backend)
	renderAnsibleRun(os.Stdout, backend, rctx, time.Since(start))

	// Record BEFORE the failure check so a failed converge still narrates.
	recordAnsibleState(rootDir, appCfg, backend, rctx.Result(), runErr)
	if runErr != nil {
		return runErr
	}
	return ansibleFailure(appCfg, rctx.Result())
}

// ansibleFailure distills a lifecycle result into the subsystem's fail-loud
// contract: a failed REQUIRED play fails the phase. Plays declared
// required: false are advisory — their failures are recorded and narrated by
// recordAnsibleState but never exit nonzero.
func ansibleFailure(appCfg *config.Config, result *runtime.LifecycleResult) error {
	if result == nil {
		return nil
	}
	requiredFailed := 0
	for _, ar := range result.Actions {
		if ar.Success {
			continue
		}
		if p, ok := appCfg.Ansible.PlaybookByID(ar.Name); !ok || p.IsRequired() {
			requiredFailed++
		}
	}
	if requiredFailed > 0 {
		return fmt.Errorf("ansible: %d/%d plays failed", requiredFailed, len(result.Actions))
	}
	return nil
}

// recordAnsibleState persists the ansible subsystem outcome + {ansible.*} facts.
// Recording never fails the converge. Required reflects whether the failure
// gates the pipeline: false only when every failed play is advisory.
func recordAnsibleState(rootDir string, appCfg *config.Config, backend *ansible.Backend, result *runtime.LifecycleResult, runErr error) {
	agg := backend.Aggregate()
	outcome, reason := "success", ""
	switch {
	case runErr != nil:
		outcome, reason = "failed", runErr.Error()
	case agg.Unreachable > 0 || agg.Failed > 0:
		outcome = "failed"
		reason = fmt.Sprintf("%d unreachable, %d failed of %d hosts", agg.Unreachable, agg.Failed, agg.Total)
	}
	required := runErr != nil || outcome != "failed" || ansibleFailure(appCfg, result) != nil
	facts := map[string]string{
		"total":     strconv.Itoa(agg.Total),
		"converged": strconv.Itoa(agg.Converged),
		"changed":   strconv.Itoa(agg.Changed),
	}
	if agg.Unreachable > 0 {
		facts["unreachable"] = strconv.Itoa(agg.Unreachable)
	}
	if agg.Failed > 0 {
		facts["failed"] = strconv.Itoa(agg.Failed)
	}
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "ansible", Attempted: true, Completed: true, Required: required,
			Outcome: outcome, Reason: reason,
			Results: facts,
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}
}

// renderAnsibleRun renders the per-play results box: image identity, then one
// row per play with its host rollup; failures tail their output.
func renderAnsibleRun(w *os.File, backend *ansible.Backend, rctx *runtime.RuntimeContext, elapsed time.Duration) {
	color := output.UseColor()
	sec := output.NewSection(w, "Ansible", elapsed, color)
	if backend.ImageRef() != "" {
		sec.Row("image  %s", backend.ImageRef())
	}

	plays := backend.PlayResults()
	if len(plays) == 0 {
		if rctx.DryRun {
			if plan := rctx.Plan(); plan != nil {
				for _, a := range plan.Actions {
					sec.Row("%s  %s", a.Name, a.Description)
				}
			}
		} else {
			sec.Row("no plays executed")
		}
		sec.Close()
		return
	}

	result := rctx.Result()
	for i, p := range plays {
		agg := ansible.AggregateResults([]ansible.PlayResult{p})
		status := "success"
		suffix := fmt.Sprintf("  %d/%d hosts, %d changed", agg.Converged, agg.Total, agg.Changed)
		if p.Check {
			suffix += " (check)"
		}
		if len(p.FailedHosts()) > 0 || p.ExitCode != 0 {
			status = "failed"
			if play, ok := cfg.Ansible.PlaybookByID(p.ID); ok && !play.IsRequired() {
				status = "warning"
				suffix += " (advisory — required: false)"
			}
		}
		output.RowStatus(sec, fmt.Sprintf("[%d/%d] %s", i+1, len(plays), p.ID), suffix, status, color)
		if status != "success" {
			if result != nil && i < len(result.Actions) && result.Actions[i].Message != "" {
				fmt.Fprintf(w, "    │   %s\n", result.Actions[i].Message)
			}
			// The play output is the evidence — a one-line message alone has
			// already cost a debugging session; tail it, never hide it.
			if p.Output != "" {
				tail := p.Output
				if len(tail) > 1200 {
					tail = tail[len(tail)-1200:]
				}
				for _, line := range strings.Split(strings.TrimSpace(tail), "\n") {
					fmt.Fprintf(w, "    │   %s\n", line)
				}
			}
		}
	}
	sec.Close()
}
