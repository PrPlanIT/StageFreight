package runtime

import (
	"github.com/PrPlanIT/StageFreight/src/ci"
)

// InvokerType identifies how StageFreight was invoked.
type InvokerType string

const (
	InvokerCI    InvokerType = "ci"
	InvokerLocal InvokerType = "local"
	InvokerAPI   InvokerType = "api" // future
)

// RuntimeContext holds all state for a lifecycle invocation.
// Three types of state are explicitly separated:
//   - Declarative: from .stagefreight.yml (lives in config.Config)
//   - Runtime: from environment variables (resolved during Prepare)
//   - Resolved: computed ephemeral state (populated in Prepare, cleaned in Cleanup)
type RuntimeContext struct {
	CI       *ci.CIContext // existing CI/local detection
	Invoker  InvokerType   // ci | local | api
	RepoRoot string        // workspace root

	// DryRun controls whether Execute phase is skipped.
	DryRun bool

	// Resolved state — populated during Prepare phase, cleaned up after.
	Resolved ResolvedState

	// Output of Plan and Execute phases — set by RunLifecycle, read by caller for rendering.
	plan   *LifecyclePlan
	result *LifecycleResult
}

// Plan returns the lifecycle plan (output of Plan phase). Nil if not yet planned.
func (r *RuntimeContext) Plan() *LifecyclePlan { return r.plan }

// Result returns the lifecycle result (output of Execute phase). Nil on dry-run or error.
func (r *RuntimeContext) Result() *LifecycleResult { return r.result }

// ResolvedState holds ephemeral state created during the Prepare phase.
// Everything here is temporary and must be cleaned up.
type ResolvedState struct {
	KubeconfigPath string // isolated tmpfile (gitops)
	CAPath         string // decoded CA tmpfile if from B64

	Backend      LifecycleBackend // selected backend instance
	cleanupFuncs []func()
}

// AddCleanup registers a function to be called during Cleanup phase.
func (r *ResolvedState) AddCleanup(fn func()) {
	r.cleanupFuncs = append(r.cleanupFuncs, fn)
}

// Cleanup runs all registered cleanup functions in reverse order.
func (r *ResolvedState) Cleanup() {
	for i := len(r.cleanupFuncs) - 1; i >= 0; i-- {
		r.cleanupFuncs[i]()
	}
	r.cleanupFuncs = nil
}

// DetectInvoker determines the invocation context from CIContext.
func DetectInvoker(ciCtx *ci.CIContext) InvokerType {
	if ciCtx != nil && ciCtx.IsCI() {
		return InvokerCI
	}
	return InvokerLocal
}
