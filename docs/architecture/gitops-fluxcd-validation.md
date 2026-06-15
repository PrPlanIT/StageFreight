# StageFreight GitOps (Flux) Validation — Per-Kustomization Verdicts

> **Status: living design document.** The durable home for how StageFreight validates a Flux GitOps
> repository in audition and how those results drive reconcile in perform. Iterate it *here*.

> **Scope: gitops/fluxcd only.** This is NOT the general `audition-proofs` framework (required vs
> advisory classification + pipeline gating across *all* audition checks — crucible, freshness, osv,
> …). That framework is a separate, deferred design. Where a Flux verdict needs to *gate a pipeline
> or merge*, it hands off to audition-proofs as one input among many. Everything in this document
> lives inside the gitops domain: `BuildRoots`, `ReconcileOrder`, `dependsOn`, Flux reconcile.

> **Conceptual model: settled (this session).** The unit of truth is the **Flux Kustomization**, not
> the repository and not the build path. Validation produces `map[KustomizationKey]Verdict`. The
> verdict has exactly two consumers, split by context. **Implementation: in progress** (Increments
> 1–3 below); Increment 4 (gating) is deferred to audition-proofs.

## Actors (the boundary everything hangs on)

Three actors, and StageFreight must not drift between them:

1. **Git** — declares desired state. Source of truth.
2. **Flux** — owns convergence. Reconciliation authority.
3. **StageFreight** — *accelerates* reconciliation and provides *earlier feedback*. Actor #3.

StageFreight's only real authorities are **(a) its own pipeline** (pass/fail, gate a merge) and
**(b) trigger a reconcile *now* instead of letting Flux poll.** It cannot prevent Flux from
eventually reconciling committed state, and it must never substitute a different state for Git's
(no "last-known-good" deployment — that is release-management with its own trust surface, explicitly
out of scope here).

This boundary *decides* the rest: a verdict can drive only two actions — "fail/gate the pipeline?"
and "accelerate this reconcile now?". Anything else is drift into actor #2.

## The unit of truth: the Flux Kustomization

A Flux GitOps repo is a set of `Kustomization` objects (`kustomize.toolkit.fluxcd.io`), each with a
`spec.path` (a build root) and `dependsOn` edges. The unit of truth is the **Kustomization
(`KustomizationKey` = namespace/name)** because that is what Flux converges, what carries the
dependency graph, what `flux reconcile kustomization` targets, and what the operator sees in
`flux get kustomizations`.

The **build path** is the *validation input*, not the unit of truth. Paths and Kustomizations are
not 1:1 (two Kustomizations may share a path; paths may nest). So:

> **Validate per unique build path** (dedup'd, efficient). **Attribute the verdict to the
> Kustomization(s) that consume that path.** The path is the mechanism; the Kustomization is the
> truth.

This lands every verdict on `KustomizationKey` — the exact key `ReconcileOrder` and the `dependsOn`
graph already use, so the data model is just:

```go
type Verdict struct {
    Status  Status   // Pass | Warn | Fail
    Reasons []string // human-readable: schema error, render failure, cycle, dangling dependsOn, …
}
// ValidateManifests → map[KustomizationKey]Verdict
```

## Graph-integrity proofs localize — there is no separate repo tier

The checks that *look* repository-level do not need a repository-level verdict; they **attribute back
to specific Kustomizations**:

- **render / schema** (`kustomize build`, `kubeconform`) → the Kustomization(s) using that path.
- **dependency cycle** A→B→A → the cycle **members** (A, B).
- **dangling `dependsOn`** (references a Kustomization not in the graph) → the **referrer**.
- **resource conflict** (two roots emit a clashing resource) → the two **producers**.

So the model is **one tier: per-Kustomization**, with graph-wide checks as *inputs* that emit
per-Kustomization verdicts. The only irreducibly whole-repo failure is "the Flux graph cannot be
discovered/parsed at all" — and that is degenerate: with no roots there is nothing to accelerate.

## The two consumers (split by context)

The same `map[KustomizationKey]Verdict` feeds two places. **Enforcement is meaningful only
pre-merge**, because of the actor boundary: post-merge, Flux will reconcile committed state on its
own poll regardless, so withholding reconcile cannot *prevent* bad state — it only delays it and
strands the good roots.

### Pre-merge (merge request)

- audition validates (hermetic — no cluster credentials).
- The aggregate verdict gates the **pipeline / merge** (advisory or required). This is where a bad
  Renovate bump is stopped *before it lands*.
- **No reconcile** (perform is gated to accepted state — see `ReconcileOrder` + the default-branch
  gate).
- The advisory-vs-**required** decision is **not gitops-specific** — it is the general
  audition-proofs framework applied to a Flux verdict. **Deferred to audition-proofs (Increment 4).**

### Post-merge (default branch)

- audition validates; perform reads the verdict map and **accelerates the non-`Fail` roots in
  `ReconcileOrder`, declines the `Fail` roots** ("skipped (invalid): … — Flux will reconcile on
  poll").
- **Fail-closed on unvalidated state.** perform accelerates a root ONLY when it
  has an explicit non-fail verdict. A *missing* verdict, an unreadable/absent
  artifact, or a whole-run `Skipped` (a tool was unavailable, so nothing was
  structurally validated) all decline — never silently "go ahead". A pass verdict
  produced under a skipped run is not trusted. StageFreight never accelerates
  state it could not verify; Flux still reconciles declined roots on its poll.
- **Always per-Kustomization skip-invalid. Never all-or-nothing. Never override Git.**
  - *Never all-or-nothing*, because withholding a valid root is downtime for a minor unrelated
    failure, and imposing atomicity Flux does not natively provide is actor #2 drift.
  - Dependents of a failed root are safe to accelerate anyway: Flux's own `dependsOn` holds them
    until the dependency is healthy. The graph informs *efficiency* (skip pointless triggers); Flux
    enforces *safety*.

**Acceleration policy is a future seam, deliberately not abstracted yet.** What perform does with a
FAIL is an *operator policy* that consumes the (durable) evidence — distinct from the evidence
itself. Today only one policy is real (skip-invalid), so it stays hardcoded inline in
`FluxBackend.Plan`; abstracting an interface before a second consumer exists would be premature
(the litmus test — "can you name two real consumers?" — fails today). When `permissive` (accelerate
all, ignore FAIL) or `strict` (any FAIL → accelerate nothing) become concrete needs, extract a
`gitops.acceleration.policy` knob. The evidence model (`proof-results.json`) is the durable decision;
the policy consuming it is where variability belongs and may evolve.

**Open question (gates Increment 3):** is there a real operator scenario that wants "if any root in
this commit is bad, accelerate *nothing*"? Current position: **no** — post-merge never blocks. If a
genuine coordinated-rollout case appears, it would be the one reason to reintroduce a post-merge
strict mode; until then it is a footgun (downtime + role-drift) we do not offer.

## Increments

- **0 (shipped, `3b4a72e`):** hermetic validation runs in audition (advisory), per build-root,
  producing findings; topological reconcile ordering; reconcile bound to accepted state.
- **1 (in progress):** `gitops.ValidateManifests → map[KustomizationKey]Verdict`. Validate per build
  path, attribute to Kustomizations; graph-integrity emits per-Kustomization verdicts. The
  audition-side renderer (today the `flux-validate` lint module) becomes a thin consumer.
- **2:** audition writes the verdict map to the `.stagefreight/` audition→perform handoff.
- **3:** perform consumes the handoff and does post-merge skip-invalid reconcile.
- **4 (deferred → audition-proofs):** pre-merge required/advisory gating. Not gitops-specific.
