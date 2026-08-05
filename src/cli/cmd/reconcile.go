package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/docker"
	_ "github.com/PrPlanIT/StageFreight/src/docker" // register compose backend
	_ "github.com/PrPlanIT/StageFreight/src/gitops" // register flux backend
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/spf13/cobra"
)

var (
	reconcileGlobalDry bool
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile infrastructure to declared state",
	Long: `Universal lifecycle reconciliation trigger.

Reads lifecycle.mode from .stagefreight.yml and dispatches to the
configured backend (flux, compose, etc.). All intelligence lives
in StageFreight — CI and CLI are just transports.

Examples:
  stagefreight reconcile
  stagefreight reconcile --dry-run`,
	RunE: runReconcile,
}

func init() {
	reconcileCmd.Flags().BoolVar(&reconcileGlobalDry, "dry-run", false, "show plan without executing")
	rootCmd.AddCommand(reconcileCmd)
}

func runReconcile(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Ansible converge set FIRST — substrate before cluster state (same order
	// as the perform runner); coexists with the primary mode.
	// Continue-then-fail: a failed converge is held, not returned — the
	// primary reconcile still runs (if a converge died mid-drain, manifest
	// sync is exactly what should still happen), then the held failure
	// surfaces in the combined error.
	var ansibleErr error
	if cfg.Ansible.HasConvergePlaybooks() {
		ansibleErr = runAnsibleConverge(cmd.Context(), cfg, rootDir, reconcileGlobalDry)
	}
	return errors.Join(ansibleErr, runPrimaryReconcile(cmd.Context(), rootDir, reconcileGlobalDry))
}

// runPrimaryReconcile executes the lifecycle.mode backend (flux/compose) —
// everything reconcile does minus the ansible subsystem, which its callers
// dispatch themselves (bare CLI above, the CI perform runner directly).
func runPrimaryReconcile(ctx context.Context, rootDir string, dryRun bool) error {
	start := time.Now()

	mode := cfg.Lifecycle.Mode
	if mode == "" {
		return fmt.Errorf("lifecycle.mode not set in .stagefreight.yml")
	}

	// Build runtime context.
	ciCtx := ci.ResolveContext()
	rctx := &runtime.RuntimeContext{
		CI:       ciCtx,
		Invoker:  runtime.DetectInvoker(ciCtx),
		RepoRoot: rootDir,
		DryRun:   dryRun,
	}

	// RunLifecycle: Resolve → Validate → Prepare → Plan → Execute → Cleanup.
	if err := runtime.RunLifecycle(ctx, cfg, rctx); err != nil {
		return err
	}

	// Report — dispatch rendering by mode.
	plan := rctx.Plan()
	result := rctx.Result()
	w := os.Stdout
	color := output.UseColor()
	elapsed := time.Since(start)

	// Staged Tools box — the tools the lifecycle prepared (flux/kubectl), in front of
	// the reconcile render box. The context carries the run ledger in the perform path
	// (reconcileRunner sets it); no-op for a bare CLI reconcile with no ledger.
	provision.StageBox(ctx, w, color)

	switch cfg.Mode().Name {
	case config.ModeGitops:
		renderGitopsPlan(w, plan, result, rctx.DryRun, elapsed, color)
	case config.ModeDocker:
		if rctx.DryRun || result == nil {
			docker.RenderPlan(w, plan, elapsed, color)
		} else {
			docker.RenderPlan(w, plan, elapsed, color)
			docker.RenderResult(w, plan, result, elapsed, color)
		}
	default:
		return fmt.Errorf("no renderer for lifecycle mode: %q", mode)
	}

	// Record the reconcile subsystem + its facts ({reconcile.*}) into cistate —
	// the gitops lines of the union summary render from these. Recorded BEFORE
	// the failure check so a failed reconcile still narrates what happened.
	// Dry-run records nothing (a plan is not an outcome). Gitops only for now:
	// the union body's line says "on {reconcile.cluster}", which the gitops
	// facet guarantees; the compose modality gets its own facts + line when its
	// vocabulary is designed.
	if !rctx.DryRun && cfg.Mode().Name == config.ModeGitops {
		recordReconcileState(rootDir, cfg, plan, result)
	}

	// Check for failures
	if result != nil {
		for _, ar := range result.Actions {
			if !ar.Success {
				failed := 0
				for _, a := range result.Actions {
					if !a.Success {
						failed++
					}
				}
				return fmt.Errorf("%d/%d actions failed", failed, len(result.Actions))
			}
		}
	}

	return nil
}

// recordReconcileState persists the reconcile subsystem's outcome + structured
// facts. Recording never fails the reconcile.
func recordReconcileState(rootDir string, appCfg *config.Config, plan *runtime.LifecyclePlan, result *runtime.LifecycleResult) {
	facts, outcome, reason := reconcileFacts(appCfg, plan, result)
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "reconcile", Attempted: true, Completed: true, Required: true,
			Outcome: outcome, Reason: reason,
			Results: facts,
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}
}

// reconcileFacts flattens a reconcile plan/result into the {reconcile.*} fact map:
// counts (total/succeeded/failed), declined (only when >0, so the line elides at
// zero), the cluster, the backend's unit noun, and per-action failure rows for
// the {failures} producer. A nil result (nothing executed) records the plan size
// with zero outcomes.
func reconcileFacts(appCfg *config.Config, plan *runtime.LifecyclePlan, result *runtime.LifecycleResult) (facts map[string]string, outcome, reason string) {
	total, declined := 0, 0
	backend := ""
	if plan != nil {
		total = len(plan.Actions)
		declined = len(plan.Declined)
		backend = plan.Backend
	}
	succeeded, failed := 0, 0
	var failureRows []string
	if result != nil {
		for _, ar := range result.Actions {
			if ar.Success {
				succeeded++
				continue
			}
			failed++
			row := ar.Name
			if ar.Message != "" {
				row += " — " + ar.Message
			}
			failureRows = append(failureRows, row)
		}
	}

	facts = map[string]string{
		"total":     strconv.Itoa(total),
		"succeeded": strconv.Itoa(succeeded),
		"failed":    strconv.Itoa(failed),
		"backend":   backend,
		"units":     backendUnits(backend),
	}
	if cluster := strings.TrimSpace(appCfg.GitOps.Cluster.Name); cluster != "" {
		facts["cluster"] = cluster
	}
	if declined > 0 {
		facts["declined"] = strconv.Itoa(declined)
	}
	if len(failureRows) > 0 {
		facts["failures"] = strings.Join(failureRows, "\n")
	}

	outcome = "success"
	reason = ""
	if failed > 0 {
		outcome = "failed"
		reason = fmt.Sprintf("%d of %d %s failed to reconcile", failed, total, backendUnits(backend))
	}
	return facts, outcome, reason
}

// backendUnits is the backend's own noun for its reconcile units — recorded as
// DATA so body copy stays backend-neutral ("Converged 14/14 kustomizations").
func backendUnits(backend string) string {
	switch backend {
	case "flux":
		return "kustomizations"
	case "compose":
		return "services"
	default:
		return "targets"
	}
}

// renderGitopsPlan renders gitops plan/result using Section output.
// Extracted from gitops.go for reuse by the universal reconcile command.
func renderGitopsPlan(w *os.File, plan *runtime.LifecyclePlan, result *runtime.LifecycleResult, dryRun bool, elapsed time.Duration, color bool) {
	sec := output.NewSection(w, "Reconcile", elapsed, color)

	if plan != nil && plan.Notes["auth"] != "" {
		sec.Row("auth   %s", plan.Notes["auth"])
	}

	if plan == nil || (len(plan.Actions) == 0 && len(plan.Declined) == 0) {
		sec.Row("No affected kustomizations — nothing to reconcile.")
		sec.Close()
		return
	}

	succeeded := 0
	failed := 0

	for i, action := range plan.Actions {
		status := "success"
		suffix := ""

		if dryRun {
			suffix = " (dry-run)"
		} else if result != nil && i < len(result.Actions) {
			ar := result.Actions[i]
			if !ar.Success {
				status = "failed"
				failed++
			} else {
				succeeded++
			}
			if ar.Duration > 0 {
				suffix = fmt.Sprintf(" (%s)", ar.Duration.Truncate(100*time.Millisecond))
			}
		} else {
			succeeded++
		}

		label := fmt.Sprintf("[%d/%d] %s", i+1, len(plan.Actions), action.Name)
		output.RowStatus(sec, label, suffix, status, color)

		if !dryRun && result != nil && i < len(result.Actions) && !result.Actions[i].Success && result.Actions[i].Message != "" {
			fmt.Fprintf(w, "    │   %s\n", result.Actions[i].Message)
		}
	}

	// Declined — failed audition validation; not accelerated (skip-invalid).
	for _, d := range plan.Declined {
		output.RowStatus(sec, fmt.Sprintf("declined %s", d.Name), "", "warning", color)
		if d.Description != "" {
			fmt.Fprintf(w, "    │   %s\n", d.Description)
		}
	}

	sec.Separator()
	if dryRun {
		sec.Row("%d actions planned (dry-run)", len(plan.Actions))
	} else {
		sec.Row("%d/%d succeeded", succeeded, len(plan.Actions))
	}
	if len(plan.Declined) > 0 {
		sec.Row("%d declined — failed validation; Flux will reconcile on poll", len(plan.Declined))
	}
	sec.Close()
}
