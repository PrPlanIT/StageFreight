# StageFreight Runtime Specification

> StageFreight is a lifecycle runtime that interprets declarative repository intent,
> resolves runtime context from its environment, dispatches to pluggable execution
> backends, and presents structured, authoritative output. CI, UI, and other callers
> are transports/frontends only; they do not own lifecycle logic.

## Execution Phases

Every lifecycle invocation passes through these phases in strict order.
Phases are **enforced by code** — backends cannot skip them, callers cannot reorder them.

| Phase | Owner | Purpose |
| ----- | ----- | ------- |
| Load | Runtime | Parse `.stagefreight.yml` |
| Resolve | Runtime | Build RuntimeContext (CI/local/UI, invoker detection) |
| Validate | Runtime + Backend | Check backend exists, capabilities met, config complete |
| Prepare | Backend | Set up execution environment (kubeconfig, docker context, etc.) |
| Plan | Backend | Compute what will be done (impact analysis, build plan, etc.) |
| Execute | Backend | Dispatch planned actions |
| Report | Runtime | Render structured output from Plan/Result |
| Cleanup | Runtime + Backend | Remove ephemeral state (always runs, even on error) |

**Dry-run**: phases run through Plan, then Report renders the plan without calling Execute.

### Phase ↔ Capability Binding

Each phase declares required capabilities. The runtime validates phase-capability
compatibility **before execution begins** — not when the phase runs.

| Phase | Required Capabilities |
| ----- | ----- |
| Prepare | `CapClusterAuth` (if cluster config present), `CapForgeAuth` (if forge config present) |
| Plan | `CapPlanExecute`, `CapImpactAnalysis` (if impact-driven) |
| Execute | `CapReconcile` (gitops), mode-specific |
| Report | `CapStructuredProgress` (if incremental output requested) |

A backend that declares `CapReconcile` but not `CapPlanExecute` is invalid for
any mode that requires Plan → Execute separation. This is caught at Validate,
not discovered at runtime.

## Backend Lifecycle Contract

Every backend implements the full `LifecycleBackend` interface:

```go
type LifecycleBackend interface {
    Name() string
    Capabilities() []Capability
    Validate(ctx, cfg, rctx) error
    Prepare(ctx, cfg, rctx) error
    Plan(ctx, cfg, rctx) (*LifecyclePlan, error)
    Execute(ctx, plan, rctx) (*LifecycleResult, error)
    Cleanup(rctx)
}
```

Backends participate in **all** phases — not just Execute.

### Rules

1. Backends **must not** write to stdout/stderr. They return `LifecyclePlan` and
   `LifecycleResult` — the runtime owns all output rendering.
2. Backends **must not** mutate global state (default kubeconfig, global env vars,
   persistent files). All state goes through `rctx.Resolved`.
3. Backends **must** register cleanup functions for ephemeral resources
   via `rctx.Resolved.AddCleanup()`.

### Plan Determinism

Given identical declarative config and runtime inputs, `Plan()` must produce
identical output. This is required for:

- Dry-run fidelity (dry-run plan == actual execution plan)
- UI preview accuracy
- CI reproducibility
- Debug trust

Non-determinism in Plan is a bug, not a feature. If a backend cannot guarantee
deterministic planning (e.g., external state dependency), it must document
the non-deterministic inputs explicitly in the plan output.

### Idempotency

Backends implementing reconciliation semantics must ensure repeated execution
converges to the same state without side effects. Specifically:

- Running Execute twice with the same plan must not cause drift
- Partial failure followed by retry must not corrupt state
- The system must be safe to re-run at any time

This is fundamental to GitOps correctness, CI retry safety, and drift detection.
Backends that cannot guarantee idempotency must declare it explicitly and the
runtime must warn on repeated invocation.

## Capability Model

Backends declare capabilities. The runtime validates required capabilities
during the Validate phase — failures are caught early, not at execution time.

```go
CapReconcile          // can reconcile GitOps resources
CapDryRun             // supports dry-run mode
CapImpactAnalysis     // can compute change impact
CapClusterAuth        // requires cluster authentication
CapForgeAuth          // requires forge authentication
CapStructuredProgress // can report incremental progress
CapPlanExecute        // supports plan/execute separation
```

Required capabilities are derived from config + context:
- gitops mode with cluster config → `CapClusterAuth` + `CapReconcile`
- `--dry-run` flag → `CapDryRun`

Unimplemented backends fail at resolve time. Never silent fallback.

## RuntimeContext

Three types of state, explicitly separated:

| Type | Examples | Source |
| ---- | -------- | ------ |
| Declarative | cluster name, server, backend, audience | `.stagefreight.yml` |
| Runtime | CA path, OIDC token, forge credentials | Environment variables |
| Resolved | kubeconfig tmpfile, CA tmpfile, backend instance, impact targets | Computed, ephemeral |

```go
type RuntimeContext struct {
    CI       *ci.CIContext
    Invoker  InvokerType     // ci | local | api
    RepoRoot string
    Resolved ResolvedState   // populated in Prepare, cleaned in Cleanup
}
```

## Invocation Contexts

| Context | Detection | Behavior |
| ------- | --------- | -------- |
| CI | `SF_CI_PROVIDER` set | Full pipeline: auth, reconcile, report |
| Local | No SF_CI_PROVIDER | Skip cluster auth if no cluster config, dry-run by default |
| API | Explicit invoker flag | Future: same contract, different transport |

## Structured Output Contract

All output flows through the runtime's rendering layer:

- **Setup section**: context, resolved state
- **Action section**: backend-specific work (from LifecyclePlan/Result)
- **Result section**: summary, counts, status
- **Failures**: `RuntimeError` with phase context
- **Dry-run**: renders plan without execution
- **Machine-readable**: future `--output json` envelope wrapping same data

Backends return structured data. Runtime renders it. This enables future
UI/API consumers to receive the same information without screen-scraping.

### Streaming Lifecycle

The runtime emits structured events at defined points during execution:

1. **Plan emitted** — after Plan phase completes, before Execute begins.
   Consumers can inspect planned actions before execution starts.
2. **Action updates** — during Execute, backends supporting `CapStructuredProgress`
   provide incremental action results as each action completes.
3. **Final result** — after Execute completes, the full `LifecycleResult` is emitted.

This enables:
- CLI: progressive Section rendering as actions complete
- UI: live progress updates via event stream
- API: structured event log for async consumers

Backends that do not support `CapStructuredProgress` emit only plan and final result.
The runtime must handle both modes transparently.

## Error Model

All errors carry phase context:

```go
type RuntimeError struct {
    Phase   string
    Backend string
    Message string
    Cause   error
}
```

Phase errors propagate up with context: `"validate: flux CLI not found"`,
`"prepare: neither DUNGEON_CA_FILE nor DUNGEON_CA_B64 is set"`.

Cleanup always runs regardless of error state.

## Concurrency Policy

Serial execution by default. Ordering guarantees maintained.

Future considerations:
- Per-target parallelism (multiple kustomizations)
- Failure isolation (one target failure doesn't abort others)
- Multi-cluster sequential or parallel

## Logging Boundary

| Channel | Purpose | Consumer |
| ------- | ------- | -------- |
| Debug | Internal diagnostics, env resolution, phase timing | Developers, `--debug` flag |
| User | Section-based rendering, status, summaries | CLI users |
| Structured | JSON envelope (future) | API, UI, automation |

Backends never log directly. They return data; the runtime decides how to present it.

## API Surface

StageFreight runtime may be exposed via CLI, subprocess, or API.
Output must be streamable and structured. The `RunLifecycle()` entrypoint
is the canonical interface — all invocation paths use it.

## Lifecycle Modes

```
mode: image   → build/scan/push container images (existing build engine)
mode: gitops  → discover/impact/reconcile (Flux today, Argo future)
```

Each mode maps to a backend via config. Backend is **declared**, not inferred.

## Backend Registry

Backends register via `init()`:

```go
func init() {
    runtime.Register("gitops", "flux", &FluxBackend{})
}
```

Resolution: `(mode, backend_name) → LifecycleBackend instance`.
Unknown combinations → hard error.
