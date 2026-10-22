# Dependency Update

`stagefreight dependency update` resolves outdated dependencies, applies updates, runs verification, and generates artifacts for review or CI consumption.

**Current ecosystem support (auto-update):** Go modules, Dockerfile `FROM` tags, GitHub releases, `toolchains.want`, Cargo, npm/yarn/pnpm, pip `requirements.txt` exact (`==`) pins, and Alpine/Debian package pins interpolated from `ARG`/`ENV`. Pipfile dependencies are discovered but not applied; Poetry (`pyproject.toml`) is not yet discovered.

## CLI Usage

```
stagefreight dependency update [path] [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Resolve and report without applying changes |
| `--bundle` | `false` | Include `deps-updated.tgz` artifact |
| `--no-verify` | `false` | Skip `go test` after update |
| `--no-vulncheck` | `false` | Skip `govulncheck` after update |
| `--ecosystem` | all | Filter to specific ecosystem(s) |
| `--output` | `.stagefreight/deps` | Output directory for artifacts |
| `--policy` | `all` | Update policy: `all`, `security` |

**Exit codes:** `0` success, `1` verify failure (tests failed after update), `2` update failure.

**Arguments:** `[path]` is the root directory to scan. Defaults to the current working directory.

### Examples

```bash
# Preview what would be updated (no changes written)
stagefreight dependency update --dry-run

# Apply all updates with verification
stagefreight dependency update

# Security-only updates, skip tests
stagefreight dependency update --policy security --no-verify

# Target a specific repo path
stagefreight dependency update /path/to/repo
```

## Artifacts

On success, three files are written to the output directory:

| File | Description |
|------|-------------|
| `resolve.json` | Machine-readable resolution: all deps, versions, CVEs |
| `deps-report.md` | Human-readable summary of applied/skipped updates |
| `deps.patch` | Git diff of all changes (for CI merge request workflows) |

With `--bundle`, an additional `deps-updated.tgz` is generated containing all artifacts.

## Go Toolchain Resolution

`dependency update` needs a Go toolchain to run `go get` and `go mod tidy`. Rather than requiring Go to be pre-installed, StageFreight uses a multi-strategy resolver that tries these approaches in order:

### Strategy 1: Native (`go` in PATH)

Standard developer machine or CI stage with Go installed. Zero overhead.

### Strategy 2: Toolcache (`STAGEFREIGHT_GO_HOME` or `/toolcache/go`)

Checks `$STAGEFREIGHT_GO_HOME/bin/go` first, then `/toolcache/go/bin/go`. Designed for Kubernetes pods where an initContainer downloads the Go toolchain into a shared `emptyDir` volume before StageFreight starts. No Docker daemon, no RBAC, no pod creation required.

### Strategy 3: Container runtime (`docker`/`podman`/`nerdctl`)

Falls back to running Go inside a container. Detects `docker`, `podman`, or `nerdctl` (first found wins). Parses the `go` directive from `go.mod` (or `toolchain` directive if present) to select the matching `golang:<ver>-alpine` image.

The container runner:

- Mounts the repo root as `/src` with the module directory as the working directory
- Runs as the current user (`--user uid:gid`) to avoid root-owned file writes
- Sets `HOME`, `GOCACHE`, and `GOMODCACHE` to `/tmp` paths inside the container
- Forwards `GOPROXY`, `GOPRIVATE`, `GONOSUMDB`, `GONOPROXY`, `GOFLAGS`, `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` from the host environment
- Uses `--pull=missing` for consistent behavior across CI nodes

### Strategy 4: Error

If none of the above are available, a clear error is returned:
```
go toolchain not found: install Go, set STAGEFREIGHT_GO_HOME, or ensure a container runtime (docker/podman/nerdctl) is available
```

## Verification

After applying updates, StageFreight runs verification (unless `--no-verify` / `--no-vulncheck`):

- **`go test ./...`** on every updated module directory, using the same toolchain strategy as the update itself.
- **`govulncheck ./...`** via `go run golang.org/x/vuln/cmd/govulncheck@latest`, so it works across all strategies without requiring a pre-installed `govulncheck` binary.

## Running from the StageFreight Container Image

When running `dependency update` from the published container image (e.g., in CI), the Go toolchain is not included in the image. The container runner strategy (strategy 3) activates automatically when a Docker socket is available.

```bash
# The mount path MUST match the host path so the inner golang container
# can resolve it from the Docker daemon's perspective.
docker run --rm \
  -v /path/to/repo:/path/to/repo \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -w /path/to/repo \
  --user "$(id -u):$(id -g)" \
  --group-add "$(stat -c '%g' /var/run/docker.sock)" \
  docker.io/prplanit/stagefreight:latest-dev \
  stagefreight dependency update
```

**Key detail:** The `-v` mount path must be the real host path (not an alias like `/src`). The container runner spawns a sibling container via the Docker socket, and the Docker daemon resolves volume mounts relative to the host filesystem. A mismatched path (e.g., `-v /host/repo:/src` then the inner container tries `-v /src:/src`) will silently mount the wrong directory or fail.

For Kubernetes with DinD sidecars, ensure the workspace volume mount uses the same path in both the StageFreight container and the Docker daemon's view of the filesystem.

## Workspace Mode

Go workspaces (`go.work`) are supported. When a `go.work` file is detected at the repo root, updates use `go -C <module-dir>` with relative paths instead of changing the working directory. This works correctly across all toolchain strategies.

## Configuration

Dependency update behavior is controlled by the `lint.modules.freshness` block in `.stagefreight.yml`. See [Lint](../config/lint.md) for the freshness module, including ecosystems, severity thresholds, ignore patterns, and package rules.

## Selection Policy

StageFreight realizes the dependency-selection policy each ecosystem already declares; it does not add a competing policy language, and never duplicates a version string into `.stagefreight.yml`.

| Ecosystem | Native constraint honored |
|---|---|
| Cargo | the `Cargo.toml` requirement operator (`=`, `~`, `^`, ranges); a bare version is caret |
| Docker | tag precision (`golang:1`, `1.26`, `1.26.4`, `@sha256:`) |
| Go | `replace` marks the dependency `Pinned`: discovery skips resolution and reporting matches apply. Go has no range syntax, so StageFreight adds none |
| npm | the `package.json` operator (`^`, `~`, exact) is preserved |
| `toolchains.want` | StageFreight's own manifest, so it owns the grammar — see below |

### CandidateSet

Every consumer reads one evaluation. `Construct` (`src/dependency/candidate.go`) turns discovered facts plus config into a `CandidateSet`; `Admits(version)` is a predicate returning the admitting/excluding `PolicyLayer`: `pinned`, `native_constraint`, `ceiling` (`max_update`), `prerelease` (stable only unless the current version is a prerelease), `cooldown` (`min_release_age`). The update filter, freshness, and remediation evaluation all consume it; none re-derive policy. Policy layers only narrow — none widens the set to reach a fix.

`max_update` defaults to `major`, which imposes no ceiling beyond the native constraint. A new default gate must be a true no-op; a narrower ceiling is explicit opt-in.

### Remediation Evaluation

`EvaluateRemediation` (`src/dependency/remediation.go`) asks the CandidateSet whether each advisory's fixed-in version is admitted, and assigns one state:

| State | Meaning |
|---|---|
| `remediated` | an applied update carries the fix |
| `reachable_unapplied` | the fix is admitted but not applied |
| `blocked_by_policy` | a fix exists but declared policy excludes it — unremediable under declared policy |
| `blocked_by_cooldown` | a fix exists but is held by `min_release_age` |
| `no_fix` | no fixed-in version published |

The invariant: every remediable vulnerability is remediated under the declared policy. A pin that blocks the only fix is reported as `blocked_by_policy` — never a silent skip and never an automatic widening. Severity comes from the advisory, state from remediation, and the explanation from policy. In MR mode the merge-request body carries these verdicts, with "unremediable under declared policy" as its own block. There is no auto-merge; a person merges.

Any change to selection behavior either preserves output or makes it match declared intent, and is disclosed in output and the changelog — never silent.

## Intent Fidelity

An update must not mutate beyond its declared intent. Lock sync is scoped to the edited packages:

- **Go** — `go get <pkg>@<ver>` per updated module.
- **Cargo** — `cargo update -p <pkg>…` for only the edited packages, grouped by workspace root (members share the root `Cargo.lock`; a `[patch]` path crate is not a member). Never a bare `cargo update`: a blanket re-resolve moves the whole graph to latest-semver, and an unrelated transitive can break the build. This is a correctness invariant, not an optimization.

The cargo step reports churn as `updated N · lock I→M`, and flags `(R× ⚠)` when lockfile mutations reach 5× the intended packages, so unintended blast radius surfaces for review instead of hiding in the lock diff.

## Lockfile Regeneration Hardening

Lockfile regeneration never runs package code and never sees secrets. Node locks are regenerated through a runner with a scrubbed environment and pinned `PATH`:

| Lockfile | Command |
|---|---|
| `package-lock.json` | `npm install --package-lock-only --ignore-scripts` |
| `pnpm-lock.yaml` | `corepack pnpm install --lockfile-only --ignore-scripts` |
| `yarn.lock` | `corepack yarn install --mode=update-lockfile` with `YARN_ENABLE_SCRIPTS=false` |

Node is provisioned lazily, only when a lockfile needs syncing. A lock-sync failure fails the update.

## Toolchain Constraints

`toolchains.want.<tool>` (scalar shorthand or `{version: …}`) takes an exact version (`1.26.4`) or a suffix-contiguous wildcard (`1.26.x`, `1.x.x`, `x.x.x`). Bare partials (`1.26`) and non-trailing wildcards (`1.x.4`) are rejected; semver operators are not accepted. Selection picks the highest stable member of the line.

Config holds intent only. The resolution lives in `.stagefreight/toolchains.lock` (committed): `{name, resolved, sha256}` per tool — the Cargo.toml/Cargo.lock split. Normal runs provision the locked version and re-verify its digest; `dependency update` moves the lock to the newest member of the line (a reviewable diff) and leaves config untouched. An exact-version update bumps config and syncs the lock. A wildcard with no lock entry is marked `LockPending` so the up-to-date filter still routes it to apply, which writes the first lock. Without the lock a wildcard would re-resolve and re-trust every run, which is no integrity at all.
