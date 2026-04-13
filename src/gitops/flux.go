package gitops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

func init() {
	runtime.Register("gitops", "flux", func() runtime.LifecycleBackend {
		return &FluxBackend{}
	})
}

// FluxBackend implements runtime.LifecycleBackend for Flux CD reconciliation.
type FluxBackend struct {
	graph        *FluxGraph
	reconcileSet []KustomizationKey
	fluxBin      string // resolved flux binary path
}

func (f *FluxBackend) Name() string { return "flux" }

func (f *FluxBackend) Capabilities() []runtime.Capability {
	return []runtime.Capability{
		runtime.CapReconcile,
		runtime.CapDryRun,
		runtime.CapImpactAnalysis,
		runtime.CapClusterAuth,
		runtime.CapPlanExecute,
	}
}

// Validate checks that the flux CLI is available and cluster config is complete.
func (f *FluxBackend) Validate(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	// Resolve flux CLI via toolchain.
	fluxVer, fluxPinned := toolchain.ResolveVersion("flux", "", cfg.Toolchains.Desired)
	fluxResult, err := toolchain.Resolve(rctx.RepoRoot, "flux", fluxVer)
	if err != nil {
		if fluxPinned {
			return fmt.Errorf("flux pinned version %s failed to resolve: %w", fluxVer, err)
		}
		return fmt.Errorf("flux CLI: %w", err)
	}
	f.fluxBin = fluxResult.Path

	// Cluster config validation (only if cluster is configured — local dev skips).
	if cfg.GitOps.Cluster.Name != "" {
		if cfg.GitOps.Cluster.Server == "" {
			return fmt.Errorf("gitops.cluster.server is required when cluster.name is set")
		}
	}

	return nil
}

// Prepare builds an isolated kubeconfig for the target cluster.
// Skipped if no cluster config is present (local dev).
func (f *FluxBackend) Prepare(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	if cfg.GitOps.Cluster.Name == "" {
		return nil // local dev — no cluster auth needed
	}
	return BuildKubeconfig(cfg.GitOps.Cluster, rctx, cfg.Toolchains.Desired)
}

// Plan discovers the Flux graph, computes impact, and builds the reconcile set.
// Deterministic: identical config + inputs → identical output.
func (f *FluxBackend) Plan(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) (*runtime.LifecyclePlan, error) {
	// Discover Flux graph from repo.
	graph, err := DiscoverFluxGraph(rctx.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("discovering flux graph: %w", err)
	}
	f.graph = graph

	if len(graph.Kustomizations) == 0 {
		return &runtime.LifecyclePlan{
			Mode:    "gitops",
			Backend: "flux",
		}, nil
	}

	// Reconcile ALL kustomizations — not just impacted ones.
	// Flux reconcile is idempotent: unchanged kustomizations converge instantly.
	// Pre-filtering by changed files misses drift from manual changes,
	// failed previous reconciles, or operator mutations.
	reconcileSet := make([]KustomizationKey, 0, len(graph.Kustomizations))
	for _, ks := range graph.Kustomizations {
		reconcileSet = append(reconcileSet, ks.Key)
	}
	f.reconcileSet = reconcileSet

	// Build planned actions.
	actions := make([]runtime.PlannedAction, len(reconcileSet))
	for i, k := range reconcileSet {
		actions[i] = runtime.PlannedAction{
			Name:        k.String(),
			Description: fmt.Sprintf("reconcile source + kustomization %s", k),
			Order:       i + 1,
		}
	}

	return &runtime.LifecyclePlan{
		Mode:    "gitops",
		Backend: "flux",
		Actions: actions,
	}, nil
}

// Execute runs flux reconcile on the planned set.
// Idempotent: repeated execution converges to the same state.
func (f *FluxBackend) Execute(ctx context.Context, plan *runtime.LifecyclePlan, rctx *runtime.RuntimeContext) (*runtime.LifecycleResult, error) {
	var results []runtime.ActionResult

	for _, k := range f.reconcileSet {
		start := time.Now()
		ar := runtime.ActionResult{
			Name: k.String(),
		}

		// Reconcile source first.
		srcCmd := exec.CommandContext(ctx, f.fluxBin, "reconcile", "source", "git", "flux-system", "-n", k.Namespace)
		if out, err := srcCmd.CombinedOutput(); err != nil {
			ar.Duration = time.Since(start)
			ar.Success = false
			ar.Message = fmt.Sprintf("source reconcile failed: %s", strings.TrimSpace(string(out)))
			results = append(results, ar)
			continue
		}

		// Reconcile kustomization.
		cmd := exec.CommandContext(ctx, f.fluxBin, "reconcile", "kustomization", k.Name, "-n", k.Namespace)
		out, err := cmd.CombinedOutput()
		ar.Duration = time.Since(start)

		if err != nil {
			ar.Success = false
			ar.Message = strings.TrimSpace(string(out))
			results = append(results, ar)
			continue
		}

		ar.Success = true
		ar.Message = strings.TrimSpace(string(out))
		results = append(results, ar)
	}

	return &runtime.LifecycleResult{Actions: results}, nil
}

// Cleanup is handled by rctx.Resolved cleanup funcs registered in Prepare.
func (f *FluxBackend) Cleanup(rctx *runtime.RuntimeContext) {
	// Kubeconfig + CA tmpfiles are cleaned up via rctx.Resolved.Cleanup().
}

// FluxReconcileResult reports the outcome of reconciling one kustomization.
// Kept for backward compatibility with existing CLI output rendering.
type FluxReconcileResult struct {
	Kustomization string
	Namespace     string
	Attempted     bool
	Success       bool
	Duration      time.Duration
	Ready         bool
	Message       string
}

// Reconcile executes flux reconcile on the given kustomizations in order.
// Legacy function — new code should use FluxBackend via the runtime.
func Reconcile(rootDir string, keys []KustomizationKey, dryRun bool) []FluxReconcileResult {
	fluxResult, err := toolchain.Resolve(rootDir, "flux", "")
	if err != nil {
		return []FluxReconcileResult{{Kustomization: "toolchain", Message: fmt.Sprintf("flux resolve: %v", err)}}
	}
	fluxBin := fluxResult.Path

	var results []FluxReconcileResult

	for _, k := range keys {
		res := FluxReconcileResult{
			Kustomization: k.Name,
			Namespace:     k.Namespace,
			Attempted:     true,
		}

		if dryRun {
			res.Success = true
			res.Message = "dry-run"
			results = append(results, res)
			continue
		}

		start := time.Now()

		// Reconcile source first
		srcCmd := exec.Command(fluxBin, "reconcile", "source", "git", "flux-system", "-n", k.Namespace)
		if out, err := srcCmd.CombinedOutput(); err != nil {
			res.Duration = time.Since(start)
			res.Success = false
			res.Message = fmt.Sprintf("source reconcile failed: %s", strings.TrimSpace(string(out)))
			results = append(results, res)
			continue
		}

		// Reconcile kustomization
		cmd := exec.Command(fluxBin, "reconcile", "kustomization", k.Name, "-n", k.Namespace)
		out, err := cmd.CombinedOutput()
		res.Duration = time.Since(start)

		if err != nil {
			res.Success = false
			res.Message = strings.TrimSpace(string(out))
			results = append(results, res)
			continue
		}

		res.Success = true
		res.Ready = true
		res.Message = strings.TrimSpace(string(out))
		results = append(results, res)
	}

	return results
}
