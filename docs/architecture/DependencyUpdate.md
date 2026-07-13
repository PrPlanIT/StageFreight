# Dependency Update

`stagefreight dependency update` resolves outdated dependencies, applies updates, runs verification, and generates artifacts for review or CI consumption.

**Current ecosystem support:** Go modules (`go.mod`) and Dockerfile base image tags (`FROM` directives). Additional ecosystems are planned but not yet implemented.

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

Dependency update behavior is controlled by the `lint.modules.freshness` block in `.stagefreight.yml`. See [Linter Configuration](Linter.md) for the full freshness module schema including ecosystems, severity thresholds, ignore patterns, and package rules.
