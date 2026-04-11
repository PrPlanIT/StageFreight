package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/ci/render"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// ── CI Lifecycle Phase Runners ───────────────────────────────────────────────
//
// These are the five canonical phase runners that form the public pipeline
// interface. GitLab (and any other CI provider) calls only these five commands:
//
//   stagefreight ci run audition
//   stagefreight ci run perform
//   stagefreight ci run review
//   stagefreight ci run publish
//   stagefreight ci run narrate
//
// Mode dispatch is owned here. lifecycle.mode in .stagefreight.yml determines
// which internal runner backs each phase. The YAML skeleton has no modality
// knowledge — it is a lifecycle-only transport.
//
// Phase authority:
//   audition — proves readiness (config, lint, runner panel, crucible)
//   perform  — authoritative production build or reconcile; no bootstrap semantics
//   review   — inspects perform output; not applicable for gitops/governance
//   publish  — sole phase authorized to distribute artifacts; not applicable for gitops/governance
//   narrate  — truth presentation; runs for all modes
//
// Internal runner names (deps, build, security, release, docs, validate,
// reconcile) remain as compatibility aliases in the registry.

// assertAuditionRan returns an error if cistate does not exist on disk.
// Called by every non-audition phase to enforce phase ordering. cistate is
// written by audition; its absence means audition did not run.
func assertAuditionRan(rootDir, phase string) error {
	p := filepath.Join(rootDir, cistate.StatePath)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return fmt.Errorf("missing cistate: audition must run before %s", phase)
	}
	return nil
}

// phaseNotApplicable renders a visible not_applicable result and records it
// in cistate. Used by review and publish for non-image lifecycle modes.
// Silent not_applicable is forbidden — users must see which phase did not run.
func phaseNotApplicable(rootDir, phase, mode string) error {
	start := time.Now()
	displayName := strings.ToUpper(phase[:1]) + phase[1:]
	reason := fmt.Sprintf("not applicable — lifecycle.mode=%q has no %s implementation", mode, phase)
	renderCISkip(displayName, start, reason)
	if stErr := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name:    phase,
			Outcome: "not_applicable",
			Reason:  fmt.Sprintf("lifecycle.mode=%q", mode),
		})
	}); stErr != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
	}
	return nil
}

// auditionPhaseRunner is the proving phase. Owns: CI freshness check,
// executorPreflight (full panel), runConfigPhase, lint, crucible bootstrap test.
// Dispatches to depsRunner for image mode or validateRunner for gitops/governance.
func auditionPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	// ── CI freshness check ─────────────────────────────────────────────
	// Verify the committed CI file matches what the current binary would
	// render. Runs before mode dispatch — CI freshness is universal.
	// Only enforced when running in CI (SF_CI_PROVIDER is set) and
	// ci.image is configured (render requires it).
	if ciCtx.IsCI() && appCfg.CI.Image != "" {
		rootDir := resolveWorkspace(ciCtx)
		if err := checkCIFreshness(ciCtx.Provider, rootDir, appCfg); err != nil {
			return fmt.Errorf("audition: %w", err)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return validateRunner(ctx, appCfg, ciCtx, opts)
	default: // "image" or "" — image is the default
		return depsRunner(ctx, appCfg, ciCtx, opts)
	}
}

// checkCIFreshness verifies the committed CI file matches what the current
// binary would render from .stagefreight.yml. Fails if stale or if the
// forge is not yet supported for rendering — no silent skipping.
func checkCIFreshness(forge, rootDir string, appCfg *config.Config) error {
	p, err := render.Plan(appCfg)
	if err != nil {
		return fmt.Errorf("ci freshness: %w", err)
	}

	rendered, err := render.Emit(forge, p)
	if err != nil {
		return fmt.Errorf("ci freshness: %w", err)
	}

	return render.Check(rootDir, forge, rendered)
}

// performPhaseRunner is the authoritative production build/reconcile phase.
// For image mode: official build only (no crucible, no bootstrap semantics).
// For gitops/governance: cluster reconcile.
func performPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "perform"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return reconcileRunner(ctx, appCfg, ciCtx, opts)
	default:
		return buildRunner(ctx, appCfg, ciCtx, opts)
	}
}

// reviewPhaseRunner inspects perform output. Not applicable for gitops/governance.
func reviewPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "review"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return phaseNotApplicable(rootDir, "review", mode)
	default:
		return securityRunner(ctx, appCfg, ciCtx, opts)
	}
}

// publishPhaseRunner is the sole phase authorized to distribute artifacts.
// Not applicable for gitops/governance.
func publishPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "publish"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return phaseNotApplicable(rootDir, "publish", mode)
	default:
		return releaseRunner(ctx, appCfg, ciCtx, opts)
	}
}

// narratePhaseRunner renders truth from prior phase state. Runs for all modes.
func narratePhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "narrate"); err != nil {
		return err
	}
	return docsRunner(ctx, appCfg, ciCtx, opts)
}
