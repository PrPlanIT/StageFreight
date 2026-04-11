package ci

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// RunOptions holds runtime options that can be passed from CLI flags
// or resolved from CI context. This ensures local reproducibility —
// users can pass --tag v1.2.3 instead of needing CI env vars.
type RunOptions struct {
	Tag     string // for release subsystem
	Verbose bool
}

// Runner is the function signature for subsystem runners.
type Runner func(ctx context.Context, cfg *config.Config, ciCtx *CIContext, opts RunOptions) error

// Registry maps subsystem names to their runner functions.
type Registry map[string]Runner

// ValidSubsystems returns the list of valid subsystem names.
// Canonical lifecycle phases are the primary interface.
// Legacy names (build, deps, security, docs, release, validate, reconcile)
// remain as compatibility aliases.
func ValidSubsystems() []string {
	return []string{"audition", "perform", "review", "publish", "narrate"}
}

// RunSubsystem dispatches to a subsystem runner by name.
// Returns a clear error for unknown subsystem names.
func RunSubsystem(reg Registry, subsystem string, ctx context.Context, cfg *config.Config, ciCtx *CIContext, opts RunOptions) error {
	runner, ok := reg[subsystem]
	if !ok {
		return fmt.Errorf("unknown subsystem %q (valid: %v)", subsystem, ValidSubsystems())
	}
	return runner(ctx, cfg, ciCtx, opts)
}
