# Not-Yet-Enforced Design Models

> Repo-only. These are design models, **not** invariants — they are recorded here (rather
> than in the public [Invariants](../invariants.md) page) precisely because the code that
> would violate them does not exist yet. They graduate into numbered invariants once there
> is something to enforce against.

Some constraints are known but cannot yet be enforced because the code that would
violate them does not exist. These live as design models, not invariants, until
there is something to enforce against:

- [`persistence-identity.md`](persistence-identity.md) — the persistence-handle
  algebra and the cross-phase transformation constraints a **second content
  store** must satisfy. Records a discovered coupling (the capability axis and the
  handle-representation axis must evolve together) that is real but unenforceable
  until a second store exists. Read before implementing one.

- [`../pipeline-flow.md`](../pipeline-flow.md) — **authoritative** for how the pipeline gates: the
  **audition contract** (`Outcome`/`Blocking`/`Replacement`) that audition publishes into the
  ledger and perform gates on, in-code and forge-agnostic. Control lives in the ledger; the
  forge only renders status and transports the ledger (`when: always`). The Fatal/Remediable
  mutation-safety classification and the deps-autoremediation self-heal are described there.
  Read before touching `depsRunner`, `performPhaseRunner`, `authorizePhase`, or the CI render.
  (The pure `deriveAuditionContract`/`performGate` are unit-tested — this is close to
  promotable to a hard invariant once the perform→audition edge has an enforcement test.)
  The broader stewardship vision this grew from is recorded, superseded, in
  `audition-transformation` (design note, in-repo under `architecture/plans/`).

- **Mutation safety — the second of two distribution concerns.** A distribution
  capability answers two independent questions, with one home for each: (1) *should
  this fire?* — eligibility, Invariant 7, mechanically enforced; (2) *is it safe to
  mutate?* — mutation safety, recorded here because it is not statically detectable.
  The PROPERTY: a mutation of **mutable shared state** — a rolling registry tag
  like `latest-dev`, a package channel, a repository write-back (docs/badges),
  retention/prune — must be freshness-safe, whereas **immutable** publications (a
  digest, a version-pinned tag, a `v1.2.3` release) are inherently freshness-
  independent. The abstraction is mutable shared pointer/state vs. immutable
  artifact, not any release/package concept. This is stated as a property on purpose, deliberately NOT bound to a
  mechanism: `ci.IsBranchHeadFresh` is the *current* implementation (a conservative,
  whole-operation, event-level approximation that gates the whole op on a branch
  pipeline and exempts tags), and a future per-target `MutationPolicyAllows` may make
  it precise — letting freshness-safe immutable publications through while still
  blocking rolling moves — without changing the property. It is not a hard invariant
  because "mutates rolling state" cannot be detected from the AST; it lives here
  until there is something to enforce against. Binding the rule to today's
  `IsBranchHeadFresh` mechanism would recreate the documentation trap that the
  freshness work itself removed.

- **Publish consumes transport artifacts, never original build outputs.** Only
  `ManagedRoot` (`.stagefreight/`) crosses the perform→publish job boundary, so every
  artifact a Publish capability consumes must have a *transport representation* (an
  archive) under `ManagedRoot` — binary→archive, package→archive, static-site tree→
  tar.gz. Publish never reaches back to an original build output directory (e.g. a
  `dist/` tree), which lives outside `ManagedRoot` and does not survive the boundary.
  The emerging enforcement seam is `artifact.ResolveSuccessfulBuildOutput` (returns an
  `ArtifactTransport`, manifest-sourced, never globs) — the sole path the `pages`
  publish runner uses to reach a build's output. Not yet a hard invariant: it graduates
  to a numbered one when the publish runner lands and a boundary test asserts no publish
  capability opens a path outside `ManagedRoot`. Recorded now because the resolver seam
  is the thing that keeps a future first-class tree artifact (or OCI/CAS transport) an
  internal change rather than a `pages` change.
