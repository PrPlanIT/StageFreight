# Builds & Tests

How artifacts are produced and gated: container images, binaries, and container-run
commands (docs generators, static-site builds), the cache that speeds them up, and the tests
that gate them. Builds are referenced by targets via `build:`.

Every build has a unique **`id`** that targets reference, and a **`kind`** that decides how
it is produced:

| `kind` | Produces |
|--------|----------|
| `docker` | A container image via `docker buildx` (single- or multi-platform). |
| `binary` | A compiled binary via a language builder (e.g. `go`). |
| `command` | Whatever a command emits in a container — a generated docs tree, a built static site — captured as a declared `output`. |

```yaml
builds:
  - id: myapp
    kind: docker
    platforms: [linux/amd64, linux/arm64]
    dockerfile: "Dockerfile"
    context: "."
    build_args:
      GO_VERSION: "1.25"
```

## Crucible mode

`build_mode: crucible` performs a self-proving rebuild — the image is built twice and its
layers are compared to verify reproducibility.

```yaml
builds:
  - id: myapp
    kind: docker
    build_mode: crucible
```

## Build cache

The `cache:` block controls incremental-build invalidation. With `auto_detect` on (the
default), StageFreight watches lockfiles and invalidates the dependent layers when they
change; `watch` lets you declare additional path→layer rules.

```yaml
builds:
  - id: myapp
    kind: docker
    cache:
      auto_detect: true
      watch:
        - paths: ["go.sum"]
          invalidates: ["COPY go.* ./", "RUN go mod download"]
```

## Build strategy selection

For `docker` builds, the strategy is chosen automatically from the platforms and the
targets that consume the build:

| Condition | Strategy | Behavior |
|-----------|----------|----------|
| `--local` flag | **local** | `--load` into the daemon, no push |
| Single platform + registries | **load + push** | `--load`, then `docker push` each tag |
| Multi-platform + registries | **multi-platform push** | `--push` directly (buildx can't `--load` multi-arch) |
| No registries | **local** | `--load`, default tag `stagefreight:dev` |

This is why single-platform images exist both locally and remotely (so local *and* remote
retention work), while multi-platform images are push-only.

## Reference

The blocks below are **generated per kind** (`docker`, `binary`, `command`) — exactly the
fields each accepts.

--8<-- "docs/assets/modules/config-reference.md:builds"

--8<-- "docs/assets/modules/config-reference.md:build_cache"

--8<-- "docs/assets/modules/config-reference.md:test"
