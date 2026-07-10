# Config Schema Evolution & the Crucible Bootstrap Sequence

> **Status:** design/plan. Most of this is **post-stability** and deferred — documented now
> so the reasoning isn't lost, and so changes made during alpha stay *forward-compatible*
> with it. What's implemented today is only the primitives (`--config`, `narrate.commit`,
> handoff, matchers/freshness); the bootstrap orchestration and `config migrate` are later.

## The problem

Schema-breaking config changes create a **self-hosting deadlock**: StageFreight builds its
own next image with its *current* image (crucible), and

- the **old** binary (current released image) can't parse a **new**-schema config, and
- the **new** binary can't parse the **old**-schema config.

So a schema change deadlocks SF's own release unless the transition is sequenced.

## Alpha vs. stability (current stance)

- **Alpha (now):** the schema is unstable. Breaking changes land **freely** — no in-binary
  migration, no version bump, users rewrite configs. `version: 1` is the alpha marker, not a
  stability promise. A clean single-schema loader (strict, no shim) is correct here.
- **Post-stability:** schema changes get **versioned migrations** (v1→v2…) via an explicit
  `stagefreight config migrate`. Version bumps happen at stability, not during alpha churn.

## Primitives that already exist

- **`--config <path>`** — first-class flag (`root.go`). The loader's ONE implicit lookup
  stays `.stagefreight.yml`; everything else is explicit. No auto-discovery heuristics.
- **`version: <n>` + dormant `MigrateToLatest`** (`migrate.go`) — the versioned-migration seam.
- **`narrate.commit`** — the repo-content auto-commit action.
- **Handoff / `restart_pipeline`** (`handoff.go`) — a pushed commit triggers a fresh pipeline
  (one-hop depth guard against loops).
- **Matchers (`when:`) + freshness (`IsBranchHeadFresh`)** — routing + mutation-safety gates.

## The transitional-file mechanism (the bootstrap)

A transitional config file (working name **`.stagefreight.bootstrap.yml`** — *not fixed*)
holds the **next**-schema config. It is:
- **Never auto-discovered** — reachable only via explicit `--config` (or crucible injecting
  it into pass-2). Only `.stagefreight.yml` is auto-loaded.
- **Promoted into `.stagefreight.yml`** once proven, then **deleted**. It joins the "promised"
  filename set only while it exists; nothing in code hardcodes its name.

## The crucible two-pipeline sequence

```
Pipeline #1  (previous image = OLD binary, OLD .stagefreight.yml)
  crucible : build new image → publish latest-dev; pass-2 (NEW binary, in-container)
             validates the transitional config
  promote  : commit .stagefreight.bootstrap.yml → .stagefreight.yml   (repo mutation → NARRATE)
             push (skip_ci:false)
  handoff  : restart
        ↓
Pipeline #2  (pulls latest-dev = NEW binary, reads promoted .stagefreight.yml)  → normal
```

Load-bearing facts (learned the hard way):
- **The image does not swap mid-pipeline.** The new binary only runs in the *restarted*
  pipeline #2. So pipeline #1 is entirely old-binary.
- **The first schema change is always manual.** Pipeline #1's promote can't be auto-driven,
  because the old binary predates the mechanism (and can't parse the new config to validate
  it). Every *subsequent* schema change can be automated — the then-current binary carries it.
  A mechanism cannot predate itself.
- **Atomicity is the real invariant.** Pipeline #2 must see the new image AND the new config
  *together*. A cache-masked old `latest-dev` + a promoted new config = deadlock. This is
  handled by **pull policy** (pull-always / TTL) + freshness — **never** by hardcoding image
  tags/digests into config.

## Placement principle (locked)

- **Repo-content commits** (badges, docs, **config promotion**) → **Narrate**. Inward,
  idempotent, `skip_ci`. Same `narrate.commit` machinery.
- **External distribution** (registry, pages, release tags/assets) → **Publish**. Outward.
- Corollary: *not* "all forge mutations → narrate." A release tag is distribution → publish.

## Deferred capabilities (post-stability / later)

1. **`crucible.bootstrap_config: <path>`** — an explicit field on the crucible build; its
   *presence* is the "this is for stage 2" signal. Injects `--config <path>` into **pass-2
   only** (pass-1 stays default). Explicit (no filename magic), diff-visible (no leftover-file
   footgun), reuses the existing `ExtraFlags` path (`crucible_engine.go`). **Design risk:** the
   pass-2→host-narrate seam — pass-2's validation result must reach host narrate to gate the
   promote-commit. Design this against an *actual* crucible run, not on paper.
2. **Auto-promote** — validate in pass-2 → `narrate.commit` of `bootstrap → .stagefreight.yml`
   → handoff restart. Reuses `narrate.commit` + handoff (composition, not new infra). Gated by
   `run_from` + freshness + matchers; configurable. Must guarantee the atomic image/config flip
   (pull policy).
3. **`stagefreight config migrate`** — a general, versioned old→new transform that helps
   **every** project (not just SF's crucible self-build). The removed `shim_narrate.go`
   docs→narrate logic is its **v1→v2 foundation** (in git history, not lost). The loader stays
   strict; migration knowledge lives in the explicit command. Standard pattern (terraform
   `0.13upgrade`, DB migrations).

## Forward-compatibility guidance (for changes landing during alpha)

When a change touches config schema / narrate / crucible, keep it compatible with the above:
- Route repo-content commits through **`narrate.commit`**, so the future promotion reuses it.
- **Never hardcode image tags/digests** into config — rely on matchers + freshness + pull policy.
- Keep `--config` as the **only** non-default config discovery. No auto-discovery.
- **Preserve schema-transform logic** (e.g. docs→narrate) as reusable `config migrate` material,
  not loader shims.
- **Presence-gate** any new crucible/narrate fields (harmless when absent).

## Non-goals (now)

- Version bump (alpha — break freely).
- An in-loader back-compat shim (rejected — clean single-schema loader).
- Building the crucible field / auto-promote / `config migrate` before stability.
