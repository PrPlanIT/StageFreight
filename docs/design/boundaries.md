# Architecture Boundaries

This document defines the package boundary rules for StageFreight.
These are enforced by convention; violations should be caught in code review.

## Package Roles

| Package | Role | May import |
|---------|------|------------|
| `src/artifact` | Published artifact identity and manifest (cross-cutting, no execution logic) | — (stdlib only) |
| `src/cli/cmd` | CLI adapters only — flag parsing, Cobra wiring, CI runner orchestration | everything |
| `src/build/docker` | Docker/buildx adapter: CLI execution, cosign signing, image inspection, buildx parsing, crucible engine, Dockerfile inventory; plus build orchestration (detect → plan → execute → publish) | build (parent), artifact, config, credentials, diag, output, pipeline, postbuild, registry |
| `src/build/pipeline` | Generic in-process phase runner and PipelineContext | artifact, build, config |
| `src/cistate` | Cross-job state ledger: per-subsystem fragments, merged into pipeline.json | ci |
| `src/postbuild` | Post-build integration glue (badges, retention, harbor, readme) | artifact, build, pipeline, registry |
| `src/registry` | Registry client + operational integration (verification, retention, referrers) | artifact, config, credentials, diag, retention |
| `src/build` | Shared build foundation: engine interfaces, plan/step/result, provenance model, OCI labeling, detection, tags, version | config, gitver; credentials/diag in execution paths |

## Scratch Rules

- `Scratch` is for **intra-package ephemeral state** only — keys written and read within the same package.
- Cross-package pipeline state uses **typed fields** on `PipelineContext` (`Manifest`, `BuildPlan`).
- Keys are namespaced by package prefix: `docker.*`, `binary.*`.
- Never add a Scratch key readable from a different package. Promote it to a typed field instead.

## Service Function Rule

Commands invocable from CI runners must expose an explicit request-based function.
`Ctx` lives inside the request — matches the `docker.Request` pattern already established:

```go
type XxxRequest struct {
    Ctx context.Context
    // ... all other inputs explicit, no package vars
}
func RunXxx(req XxxRequest) error { ... }
```

The Cobra `RunE` wrapper builds the request from flag vars and calls `RunXxx`.
CI runners call `RunXxx` directly — no flag var mutation.

**Any exported or testable non-Cobra function in `src/cli/cmd` must not read `cfg.*`.**
Config must arrive by parameter. The Cobra wrapper is the only global-aware edge.

## Import Audit Notes

- `src/config`: pure data model, zero internal imports — safe to import anywhere.
- `src/props`: no internal imports — stable leaf node.
- `narrator` → `props`: one-way, intentional — healthy.
- `postbuild`: high fan-in by design — acceptable as long as it stays post-build-only.
- `src/output` → `lint`: only upward coupling in output; acceptable.

## Rot Risk Watchlist

These are the areas most likely to accumulate coupling if not actively maintained.

| Area | Risk | Signal to watch for |
|------|------|---------------------|
| `src/artifact` | Foundation-package drift | Any internal imports or non-manifest execution/provider logic appearing |
| `src/postbuild` | Temporal junk drawer | New files that aren't badge/readme/retention/harbor |
| `src/build` | Domain sprawl | Files importing `forge` or `release` (strong smell) |
| `src/cli/cmd` | cfg-as-global creep | Any non-Cobra function reading `cfg.*` without a param |
| `src/registry` | Dependency regression | Any import of `src/build` (hard boundary after artifact extraction) |
| `narrator_run.go` helpers | cfg swap recurrence | Any save/restore pattern on a package global |
| `output` → `lint` | Upward coupling | output importing any domain package other than lint |

## Import Contracts

Tight boundary rules for the hottest packages. Violations should be caught in code review.
These rules are intended for future automated checking.

### `src/artifact`
- **Identity:** cross-cutting published artifact identity and manifest — not execution logic
- **Permitted:** stdlib only (zero internal imports)
- **Forbidden:** everything internal — this is a foundational leaf package
- **Rules:**
  - Owns published image/binary/archive metadata; build and registry both import it
  - Contains only data structures plus serialization/persistence directly related to
    artifact identity and manifest handling. No execution logic, orchestration,
    network I/O, or provider integration.

### `src/cli/cmd`
- **Permitted:** any service/domain package (this is the dependency graph leaf)
- **Forbidden:** must not be imported by any package
- **Rules:**
  - Non-Cobra helpers must not read package globals (cfg.*, flag vars, mutable package state)
  - Command wrappers may translate flags/config into request structs, but business logic
    should not accumulate here
  - Command wrappers must not pass partially-initialized config or mutate shared state
    before calling service functions. All inputs must be explicit.
  - Non-wrapper helpers should prefer narrow params or explicit request structs over
    wrapper-owned state fanout

### `src/build` (root package files only)
- **Identity:** shared build foundation — NOT a Docker implementation home
- **Permitted:** `config`, `gitver`, `artifact`
- **Permitted in execution/proving paths only:** `credentials`, `diag`
- **Forbidden:** `forge`, `release`, `cli`, `postbuild`
- **Forbidden in root package:** `registry`
- **Rules:**
  - Root must NOT contain CLI adapter code (docker/buildx/cosign/podman command invocation)
  - Shared build semantics (provenance model, OCI labels, plan fingerprinting) belong here
  - Docker adapter code belongs in `build/docker/`
  - Binary-specific code (gobuild.go, archive.go) stays until volume justifies `build/binary/`
- **Subpackage note:** `build/docker` and `build/pipeline` are higher-level coordinators
  and may have wider imports consistent with their orchestration role

### `src/registry`
- **Identity:** operational registry integration layer, not a pure registry client
- **Permitted:** `artifact`, `config`, `credentials`, `diag`, `retention`
- **Forbidden:** `build` (hard boundary — do not reintroduce), `forge`, `release`, `cli`, `postbuild`, `narrator`
- **Rules:**
  - Provider logic and registry-domain operations belong here
  - Registry operates on artifact types but does not define or mutate artifact identity semantics
  - Retention application is allowed here only as long as registry remains the execution
    home for registry retention operations

### `src/config`
- **Zero internal imports.** Must stay a pure data model.

### `src/props`
- **Zero internal imports.** Stable leaf node.

### `src/postbuild`
- **Identity:** post-build integration layer for completed artifacts only
- **Permitted:** `artifact`, `build`, `registry`, `config`, `output`
- **Forbidden:** `forge`, `release`, `cli`, `narrator`
- **Rule:** only work that is strictly downstream of completed build results belongs here
- **Smells (should trigger review pushback):** release authoring, pipeline triggering,
  configuration mutation, provenance generation, signing, notification fanout

## Layer Rigor

Review rules depend on which layer code belongs to. Identify the layer first.

| Layer | Example | Rules |
|---|---|---|
| Identity | `artifact.ArtifactID` | Typed; constructed only by `NewArtifactID`; propagated unchanged; never recomputed from fields |
| Truth model | `OutputsManifest`, `ResultsManifest`, `*ExecutionView` | Schema-validated; deterministic; joined only on `ArtifactID` |
| Configuration | `RegistryConfig.Provider`, `RegistryConfig.Credentials` | Authoritative for routing, auth, and policy |
| Presentation | `providerFromHost`, release-notes labels, display strings | Heuristics allowed; misclassification affects labels only |

A presentation helper is correct when a misclassification renders as a bad label and never
changes behavior. Verify that its output does not flow into routing, auth, or policy, and
record that boundary in a comment; do not prune working heuristics (e.g. substring matches
for self-hosted `harbor.*` / `gitea.*` hosts) to make a display function "safer".
Presentation must never fail a completed operation — URL and label builders degrade, never
panic.

## Git Transport

Commit, tag, and sync networking goes through one `gitstate.Transport`, chosen once per
session by `gitstate.ResolveTransport`:

- **System git** (default) — whenever a `git` binary exists and StageFreight holds no
  injected credential. It honors `~/.ssh/config`, credential helpers, agents, SSH certs,
  `ProxyJump`, and enterprise auth, which an embedded client cannot reproduce (e.g. go-git
  picks SSH keys by filename and ignores host → `IdentityFile` mappings).
- **Embedded go-git** — only when StageFreight was explicitly handed a credential to act
  as (`SSH_PRIVATE_KEY` for SSH; the owning forge's host-bound credential for HTTPS) or
  there is no git binary. An SSH agent or on-disk key is the user's environment, not an
  injected credential.

The decision is made in one place from git availability and credentials together;
implementations never re-scan the environment, and one operation never mixes transports.

The commit replay engine (`src/commit/gogit_replay.go`) rebuilds trees in-process and is
guarded by a path-set integrity check (staged paths must equal the source commit's change
paths; mismatch hard-resets). Open direction: extend the guard to tree/blob/mode equality,
or replace in-process replay with system `git rebase` for local workflows on the same
"let git do git" principle.

## On-Disk Layout

`src/paths` (stdlib-only leaf) is the single source of truth for where StageFreight
writes. Every path belongs to one bucket:

| Bucket | Location | Accessor | Lifecycle |
|---|---|---|---|
| Durable | `.stagefreight/` allowlisted entries (`scribe/`, `preset-cache/`, `inventory/`, `toolchains.lock`) | `paths.Durable` | Committed and reviewed |
| Per-run I/O | `.stagefreight/` everything else (manifests, content store, reports, security, deps, `dist/`, `pipeline.json`) | `paths.Ephemeral` | Gitignored; forwarded between CI jobs; regenerated each run |
| Scratch | `.stagefreight/.tmp/` | `paths.Scratch` | Gitignored; safe to delete |
| Cache | out of tree (`paths.ResolveCacheRoot`) | `paths.Cache` | Cross-run reuse; never a correctness dependency |
| State | `/var/lib/stagefreight/` | `paths.State` | Host-scoped (signing/KMS) |

- Per-run I/O is in-tree only because forge artifact/report paths must resolve inside the
  project directory. It stays flat at the top of `.stagefreight/` for discoverability.
- `.stagefreight/.gitignore` is a self-contained **allowlist**: `/*` ignores everything,
  then `!` lines carve out the durable set. A new output is ignored by default; a new
  durable entry must be added to the allowlist and the classifier together (a completeness
  test ties them). The commit backend plants it via `workspace.EnsureGitignore` before
  staging, so every managed repo gets it without editing its root `.gitignore`.
- Cache root priority: `--cache` → `SF_CACHE_ROOT` → `/stagefreight` if writable (the
  container bind-mount convention) → `${XDG_CACHE_HOME:-~/.cache}/stagefreight` (native)
  → none (each consumer falls back to its own ephemeral location). The Actions render
  persists `/stagefreight/cache` with `actions/cache`; `SF_CACHE_ROOT` is the seam for
  pointing the cache at a forge-cacheable in-workspace path.
- `.stagefreight/toolchains` (`src/toolchain/cache.go`) is the last-resort toolchain
  install location when no cache root resolves. It is cache (ignored, wiped by checkout),
  not durable state.

## Enforcement

Today these boundaries are enforced by **convention and code review**, not a mechanical
gate — the rules above are the review checklist. A future static check could fail CI on the
clear-cut violations (`cfg.` outside the Cobra wrappers in `src/cli/cmd`, `src/registry`
importing `src/build`, `src/artifact` importing an internal package), but that check isn't
wired yet — treat this page as the authority in the meantime.
