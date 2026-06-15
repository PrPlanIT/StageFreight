package gitops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/auditionproof"
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
	//
	// Order by the dependency graph (deps first) — not map-iteration order, which
	// is random and ignores spec.dependsOn. Topological order is deterministic
	// and mirrors how Flux itself sequences convergence.
	order := ReconcileOrder(graph)

	// ACCELERATION POLICY (currently hardcoded "skip-invalid"). This inline block
	// IS the policy that consumes the evidence — the future seam where an operator
	// knob (gitops.acceleration.policy: permissive | skip-invalid | strict) would
	// live. It is deliberately NOT abstracted yet: skip-invalid is the only real
	// consumer today, and extracting an interface before a second policy is real
	// would be premature. Extract when permissive/strict become concrete needs.
	// See docs/architecture/gitops-fluxcd-validation.md.
	//
	// Skip-invalid, FAIL-CLOSED: accelerate a kustomization ONLY when audition
	// recorded an explicit non-fail verdict for it. The verdict comes from the
	// proof-results artifact the operator reviewed — perform does NOT re-validate.
	// A failed verdict is declined with its reason. Anything UNVALIDATED is ALSO
	// declined — no artifact, validation skipped (tool unavailable), or simply no
	// verdict for this root: StageFreight never accelerates state it could not
	// verify. A missing verdict must not become "go ahead". Flux still reconciles
	// every declined root on its own poll; declining a root never withholds a
	// validated one (no all-or-nothing).
	validated, failReasons, unavailable := reconcileVerdicts(rctx.RepoRoot)

	reconcileSet := make([]KustomizationKey, 0, len(order))
	actions := make([]runtime.PlannedAction, 0, len(order))
	var declined []runtime.PlannedAction
	for _, k := range order {
		ks := k.String()
		var reason string
		switch {
		case unavailable != "":
			reason = "validation unavailable (" + unavailable + ") — not accelerated"
		case failReasons[ks] != "":
			reason = failReasons[ks] + " — declined"
		case validated[ks]:
			reconcileSet = append(reconcileSet, k)
			actions = append(actions, runtime.PlannedAction{
				Name:        ks,
				Description: fmt.Sprintf("reconcile source + kustomization %s", k),
				Order:       len(actions) + 1,
			})
			continue
		default:
			reason = "not validated — not accelerated"
		}
		declined = append(declined, runtime.PlannedAction{
			Name:        ks,
			Description: reason + "; Flux will reconcile on poll",
			Action:      "declined",
		})
	}
	f.reconcileSet = reconcileSet

	return &runtime.LifecyclePlan{
		Mode:     "gitops",
		Backend:  "flux",
		Actions:  actions,
		Declined: declined,
	}, nil
}

// reconcileVerdicts reads audition's proof results and classifies kustomizations
// for skip-invalid reconcile, FAIL-CLOSED. It distinguishes the three states that
// matter: validated-and-passed (safe to accelerate), validated-and-failed
// (declined with reason), and NOT VALIDATED. The last is reported via a non-empty
// `unavailable` when validation did not run for the repository at all (no
// artifact, unreadable, or validation skipped because a tool was missing) — in
// which case nothing should be accelerated. A per-root absence (a key not in the
// returned `validated`/`failReasons`) is likewise treated as not-validated by the
// caller. A pass verdict produced under a skipped run is NOT trusted, because
// `Skipped` short-circuits before any per-root verdict is consulted.
func reconcileVerdicts(rootDir string) (validated map[string]bool, failReasons map[string]string, unavailable string) {
	results, err := auditionproof.Read(rootDir)
	if err != nil {
		return nil, nil, "proof results unreadable"
	}
	if results.FluxValidate == nil {
		return nil, nil, "no validation evidence"
	}
	if results.FluxValidate.Skipped != "" {
		return nil, nil, results.FluxValidate.Skipped
	}
	validated = map[string]bool{}
	failReasons = map[string]string{}
	for key, v := range results.FluxValidate.Verdicts {
		if v.Status == "fail" {
			failReasons[key] = strings.Join(v.Reasons, "; ")
		} else {
			validated[key] = true
		}
	}
	return validated, failReasons, ""
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
