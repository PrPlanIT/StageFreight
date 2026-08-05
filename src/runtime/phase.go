package runtime

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Phase names — canonical, all modes.
const (
	PhaseLoad     = "load"
	PhaseResolve  = "resolve"
	PhaseValidate = "validate"
	PhasePrepare  = "prepare"
	PhasePlan     = "plan"
	PhaseExecute  = "execute"
	PhaseReport   = "report"
	PhaseCleanup  = "cleanup"
)

// RuntimeError carries phase context through the error chain.
type RuntimeError struct {
	Phase   string
	Backend string
	Message string
	Cause   error
}

func (e *RuntimeError) Error() string {
	if e.Backend != "" {
		return fmt.Sprintf("%s[%s]: %s", e.Phase, e.Backend, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Phase, e.Message)
}

func (e *RuntimeError) Unwrap() error {
	return e.Cause
}

func phaseError(phase, backend string, err error) error {
	return &RuntimeError{
		Phase:   phase,
		Backend: backend,
		Message: err.Error(),
		Cause:   err,
	}
}

// RunLifecycle is the single entrypoint for all lifecycle modes.
// CLI commands call this — they do NOT orchestrate phases themselves.
//
// Phases execute in strict order:
//
//	Resolve → Validate → Prepare → Plan → [Execute] → Report → Cleanup
//
// Load phase is the caller's responsibility (config is already loaded).
// Cleanup ALWAYS runs, even on error.
// Dry-run skips Execute and renders the Plan instead.
func RunLifecycle(ctx context.Context, cfg *config.Config, rctx *RuntimeContext) error {
	// --- Resolve mode → backend name ---
	mode := cfg.Lifecycle.Mode
	if mode == "" {
		return phaseError(PhaseResolve, "", fmt.Errorf("lifecycle.mode not set in config"))
	}
	lm, ok := config.LookupMode(mode)
	if !ok {
		return phaseError(PhaseResolve, "", fmt.Errorf("unknown lifecycle.mode %q", mode))
	}

	// Resolve the backend name from the canonical mode table (single source of truth):
	// gitops→GitOps.Backend, docker→Docker.Backend. image/governance have no RunLifecycle
	// backend (Backend nil) → empty name → ResolveBackend fails with the right error.
	var backendName string
	if lm.Backend != nil {
		backendName = lm.Backend(cfg)
	}

	return RunLifecycleBackend(ctx, cfg, rctx, lm.Name, backendName)
}

// RunLifecycleBackend runs the phase sequence against an explicitly named
// (mode, backend) pair, independent of cfg.Lifecycle.Mode. Presence-gated
// subsystems that coexist with the repo's primary mode (ansible convergence
// alongside gitops) dispatch through here; RunLifecycle is the mode-derived
// front door that delegates to it.
func RunLifecycleBackend(ctx context.Context, cfg *config.Config, rctx *RuntimeContext, mode, name string) error {
	backend, err := ResolveBackend(mode, name)
	if err != nil {
		return phaseError(PhaseResolve, name, err)
	}
	return RunLifecycleWith(ctx, cfg, rctx, mode, backend)
}

// RunLifecycleWith runs the phase sequence against a caller-constructed backend
// instance — the seam for pre-phase scoping a registry constructor can't carry
// (e.g. `ansible run <id>` selecting one play before Validate).
func RunLifecycleWith(ctx context.Context, cfg *config.Config, rctx *RuntimeContext, mode string, backend LifecycleBackend) error {
	// Cleanup always runs — register before any work.
	defer rctx.Resolved.Cleanup()

	rctx.Resolved.Backend = backend

	// --- Validate ---
	// Check capabilities first.
	required := DeriveRequired(mode, cfg, rctx)
	if err := ValidateCapabilities(backend, required); err != nil {
		return phaseError(PhaseValidate, backend.Name(), err)
	}
	// Backend-specific validation.
	if err := backend.Validate(ctx, cfg, rctx); err != nil {
		return phaseError(PhaseValidate, backend.Name(), err)
	}

	// --- Prepare ---
	if err := backend.Prepare(ctx, cfg, rctx); err != nil {
		return phaseError(PhasePrepare, backend.Name(), err)
	}

	// --- Plan ---
	plan, err := backend.Plan(ctx, cfg, rctx)
	if err != nil {
		return phaseError(PhasePlan, backend.Name(), err)
	}
	plan.DryRun = rctx.DryRun

	// --- Execute (skipped on dry-run) ---
	var result *LifecycleResult
	if !rctx.DryRun {
		result, err = backend.Execute(ctx, plan, rctx)
		if err != nil {
			return phaseError(PhaseExecute, backend.Name(), err)
		}
	}

	// --- Report ---
	// Plan and result are returned for the caller to render.
	// The runtime's report phase stores them on the context for the caller.
	rctx.plan = plan
	rctx.result = result

	return nil
}
