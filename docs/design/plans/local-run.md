# `stagefreight run` — Local Lifecycle Orchestrator

> **Status:** design, not built. The minimal core below is the direction. Everything under
> [Deferred options](#deferred-options) is costed reference, not a spec: each flag is built
> only when real use needs it.

## Purpose

The lifecycle exists only as five phase primitives — `stagefreight ci run
{audition,perform,review,publish,narrate}` — sequenced by the rendered CI file as separate
jobs, with `.stagefreight/pipeline.json` (`cistate`) handed between them and
`assertAuditionRan` enforcing order. There is no single local command that walks the spine.

`stagefreight run` is that command: an orchestrator over the existing primitives, **not a
second CI implementation**. It preserves the phase contract (perform creates, review
evaluates, publish externalizes) and only sequences it locally.

## Minimal core

1. Loop the existing runners in-process: `buildCIRegistry()` → `ci.RunSubsystem` per phase.
   `cistate` threads automatically, exactly as in CI. `ci run <phase>` stays the primitive.
2. Default scope is `audition → perform → review`, stopping before publish. That is a
   rehearsal by construction — it never reaches the externalizing phase, so it needs no
   mutation gating to be safe.
3. Native execution is acceptable to prove the loop; sandboxed execution follows.

No `--mode`, `--through`, `--mutate`, `--isolation`, presets, or extra config file is built
up front.

## Constraints any extension must honor

- **The publish gate is off locally.** `authorizePhase` (`phase_authz.go`) returns early when
  not in CI. Any `run` that can reach publish must re-assert the raw-outcome review gate
  itself, or a red review would externalize.
- **A rehearsal must still build.** `--dry-run` stops after Plan; review needs a real
  artifact. The seam is the buildx output args (no `--load`/`--push` vs `--load` vs `--push`),
  not skipping the build.
- **Generated-file writes are scattered** across phases with no central apply point. File
  isolation therefore works as a branch-context switch around the whole run, not per-write
  interception.

## Deferred options

### Axes

Each answers a different question; none implies another.

| Flag | Question | Values |
|---|---|---|
| `--mode` | which subsystem(s) participate | `image`, `bin`, `build`, `gitops`, `governance`, … (default: the manifest's `lifecycle.mode`) |
| `--through` | how far down the spine | `audition` · `perform` · `review` · `publish` |
| `--mutate` | which irreversible effects are permitted | `rehearse` < `local` < `commit` < `full` |
| `--isolation` | execution boundary | `container` (default) · `native` |

### Mutation level

A cumulative ladder carried on `ci.RunOptions` (never a package global); `ci run <phase>`
callers default to `full`, so CI behavior is unchanged. Below its level a chokepoint narrates
`DRY: would …` instead of acting.

| Effect | Minimum level |
|---|---|
| build in builder | `rehearse` |
| `--load` to daemon, local image retention, local cache prune | `local` |
| git commit of generated changes | `commit` |
| registry push, signing, git push, tag, forge release, remote retention | `full` |

`--mutate` has **no config counterpart**, deliberately. Subsystem behavior is already
configured in `.stagefreight.yml` (dependency automation, narrate/sync commit toggles); a
repo-level mutation ceiling would be a second policy surface for the same decisions.
`--mutate` only contains how far one local run lets those configured effects escape.
`--mutate full` is the whole pipeline for an operator with no forge; an organization that
wants CI-only publishing configures the subsystems accordingly.

### Isolation

**Isolation and cache persistence are separate concerns.** Isolation is what the build runs
inside; cache persistence is where reused state lives. `--isolation container` reproduces
the CI execution model: an ephemeral container, the workspace bind-mounted, a pinned SDK,
and the cache root bind-mounted from an explicit host path
(`${XDG_CACHE_HOME:-~/.cache}/stagefreight`) — never an opaque named Docker volume, which
accumulates invisibly. `--isolation native` (host toolchain and host caches) is an explicit
opt-out: it inherits the global module cache, host environment, tool versions, and proxy
state, so it is never the default.

### File isolation

Classify the worktree at start (`gitstate` clean/dirty):
- **Clean** — generated edits land in place; `run` reports the changed-file set as
  reversible.
- **Dirty** — snapshot the operator's uncommitted work (the replay engine's
  capture/restore autostash), run on a `sf/run-<shortsha>` branch, then restore the
  snapshot on the original branch. The quarantine branch is never pushed.

Invariant: after a run below `commit`, the operator's branch ref and uncommitted work are
byte-identical to before; StageFreight's edits are either restorable working-tree changes
(clean start) or confined to an `sf/run-*` branch.

### Phasing

1. Orchestrator + `--through` + `--mode` + `--isolation` (mutation fixed at `full`).
2. Mutation level, the chokepoint gating, and local re-assertion of the publish gate.
3. File isolation and quarantine, with `--undo`.
4. Report UX and mode vocabulary for non-build lifecycles.
