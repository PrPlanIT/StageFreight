# StageFreight — Terminal Output Layering

> **Status: living design document + active migration.** The rule for how terminal
> output is produced, and the plan to bring the codebase back to it. Companion to
> [ci-render.md](ci-render.md), which enforces the same *data-vs-mechanism* split for
> forge-native CI YAML (via Go's `internal/`). This doc is that principle for the
> **human-facing terminal surface**, where it drifted.

## The rule

The same one ci-render states for CI documents:

> **Data is the leaf of the import graph and knows nothing about presentation. The
> renderer is a separate mechanism. The two never trade places, and the boundary is
> enforced by the build — not by convention.**

Applied to terminal output:

- **Domain packages** (`gitops`, `security`, `sign`, `build`, `toolchain`, …) do work
  and return **structured results as data**. They resolve tools, compute findings,
  build ledgers-as-data — and **never render**. A domain package must not import
  `output.NewSection`/`Section` or call `provision.Render`.
- **`cli/cmd`** is the presentation layer. It takes the domain results and renders
  them — sections, tables, the `Staged Tools` ledger. Rendering lives here, and only
  here.
- The **enforcement is the import/call graph**, checked by a build-failing test — not
  a comment asking people to be good.

### Why: the drift this prevents

A domain method that renders inline mixes logic with presentation and makes the same
fact reachable two ways (structured result *and* a bespoke box), so they diverge. The
tool-provenance bug that motivated this doc is the canonical example: subsystems
resolved tools and either printed a bare inline `tools: kustomize · kubeconform` line
or dropped the provenance entirely, because *each caller* owned its own display. When
data is the leaf and cli/cmd is the only renderer, that class of bug cannot occur —
the tool either appears in the result (data) or it does not; it can never appear
*inconsistently* via an inline render.

## The primitive: `provision.StageBox` (done)

Staged tools reach the user exactly ONE way: **`provision.StageBox(ctx, w, color)`**,
called once by a phase's renderer immediately before it opens its work box
(`output.NewSection`). It drains that phase's provisioning delta from the run ledger and
renders a "Staged Tools" box **in front of** the work box — listing exactly the tools
that phase pulled. No-op when the phase pulled nothing.

Every phase uses the identical call:

- **gitops** — before the `GitOps Validation` box (`ci_runners.go`).
- **test** — before the `Test` box (`test/render.go`).
- **lint** — before both `Lint` boxes (`cli/cmd/lint.go`, `build/pipeline/phase.go`).
- **reconcile** — before the `Reconcile` box (`cli/cmd/reconcile.go`).
- **build** — before each domain box (`build/domains/run.go`); the per-phase delta is
  non-empty only for the domain that resolved (Build → go/rust), so the others no-op.

**Adding a new tool-using phase = one line**, `provision.StageBox(ctx, w, color)`,
ahead of its work box. Nothing else — the box, columns, and trust provenance come for
free. This is the "know how things should be built" convention: same call, same place.

## Tool provenance: the ctx-collector (done)

Every resolved tool surfaces without per-subsystem result plumbing via a
**request-scoped collector carried in `context`** (`provision/context.go`):

- `provision.Resolve(ctx, rootDir, tool, ver, purpose)` — resolves via
  `toolchain.Resolve` (which stays a pure leaf) AND records the tool (trust from
  `Result.Trust`, plus purpose) into the ctx ledger. Tools acquired outside `Resolve`
  (`ResolvePinned` → cargo-llvm-cov, `EnsureRustLlvmTools` → llvm-tools, and native
  substrate) use `provision.Record(ctx, …)` / `RecordCtxAll`. **All tools are covered.**
- `provision.WithLedger(ctx)` — seeded once per run (`auditionPhaseRunner` /
  `performPhaseRunner`).
- `provision.StageBox(ctx, w, color)` drains the per-phase delta (unexported
  `flushCollected`) and renders; phases never touch the flush by hand. `Collected(ctx)`
  returns the whole-run ledger for the CI artifact — the only place a *summary* lives.

`toolchain.Result` stays the pure data leaf (`Trust`, `Version`, `SourceURL`); the
engine never knows a box exists.

**This is sanctioned; the package-global ledger was not.** A `context`-scoped collector
is explicit in the signature, request-lifetime, and testable — the same idiom Go uses
for loggers/trace-spans. A *package-global* mutable ledger is hidden, process-lifetime
ambient state nobody passes — that is what we rejected, along with putting `record()`
inside `toolchain.Resolve` (engine mixing logic with observability).

## The ratchet (done)

`src/provision/render_boundary_test.go` fails the build if any package **outside an
allowlist** calls the LOW-LEVEL `provision.Render`. The blessed path is `StageBox`
(which drains the delta and calls `Render` internally — same package, invisible to the
grep), so phase renderers use `StageBox` and never appear here. The allowlist is now:

- `dependency` — **grandfathered**; renders its own one-off tool row inline.

`StageBox` itself is unguarded: it IS the convention, part of the render vocabulary like
`output.NewSection`. The low-level `Render` front can only shrink.

## The reconquista (TODO — the big refactor)

The rule is **not** the current reality. ~18 non-`cli/cmd` packages still render
inline (import `output.NewSection` or call `provision.Render`). Known inventory:

- `build/docker/*` — crucible, crucible_contributor, builder, cache, cache.go,
  cache_retention, cache_retention_external, cache_prune_buildkit (~8)
- `build/pipeline/*` — phase, summary; `build/domains/run.go`
- `postbuild/*` — readme, badges, retention (3)
- `dependency/*` — apply_go, apply_cargo, update (3)
- `test/render.go`, `docker/renderer.go`

Migration per package (mechanical but real): (1) give the domain result a data field
for whatever it renders (e.g. `[]provision.Entry`, or a typed summary struct); (2)
move the `output`/`provision.Render` calls into the corresponding `cli/cmd` runner;
(3) remove the domain package from the ratchet allowlist so the build guards it.

**Endgame:** the `provision.Render` allowlist is just `{cli/cmd}`, and an equivalent
guard covers `output.NewSection` (extend the ratchet, or move the section renderer
behind an `internal/` wall so the compiler enforces it like ci-render's backends).
Then "domain returns data, cli/cmd renders" is true by construction across the whole
terminal surface — not just the gitops exemplar.

This is a standalone initiative, deliberately **not** bundled into any single feature
change; each package migrates on its own, shrinking the allowlist one entry at a time.
