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

## The exemplar (done)

GitOps validation is the worked example of the rule:

- `gitops.ValidationMeta.Provisioned []provision.Entry` — the tools the validation
  resolved, as **data**, built with the pure mapper `provision.FromToolchain(res, purpose)`.
  `gitops` imports `provision` only for the data half; it never renders.
- `cli/cmd/ci_runners.go` calls `provision.Render(meta.Provisioned, …)` — the ledger
  is rendered in the presentation layer, right before the results box.
- `toolchain.Result` stays the pure data leaf (`Trust`, `Version`, `SourceURL`); no
  ledger, no `record()`, no knowledge that a box exists.

## The ratchet (done)

`src/provision/render_boundary_test.go` fails the build if any package **outside an
allowlist** calls `provision.Render`. The allowlist today:

- `cli/cmd` — the presentation layer (permanent).
- `test`, `dependency` — **grandfathered**, pending migration.

New inline rendering cannot be added: the front can only shrink.

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
