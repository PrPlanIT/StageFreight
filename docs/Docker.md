# StageFreight — Docker Build Configuration

How StageFreight builds, pushes, and manages container images using the
`builds:` and `targets:` sections of `.stagefreight.yml`.

> **Reference docs:** [Config Reference — builds](reference/Config.md#config-builds) · [Config Reference — targets](reference/Config.md#config-targets) · [CLI Reference — docker](reference/CLI.md#cli-stagefreight-docker)

---

## Builds

A build defines how to produce a container image. Each build has a unique
ID that targets reference.

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

Currently the only supported kind is `docker`. Future kinds may include
`helm` or `binary`.

### Build Mode: Crucible

Crucible mode performs a self-proving rebuild — the image is built twice
and the layers are compared to verify reproducibility.

```yaml
builds:
  - id: myapp
    kind: docker
    build_mode: crucible
```

---

## Targets

Targets define where build artifacts go. Each target has a `kind` that
determines its behavior.

### Registry Target

Pushes image tags to a container registry.

```yaml
targets:
  - id: dockerhub-stable
    kind: registry
    build: myapp              # references builds[].id
    url: docker.io
    provider: docker          # auto-detected from URL if omitted
    path: myorg/myapp
    tags:
      - "{version}"
      - "{major}.{minor}"
      - "latest"
    when:
      git_tags: [stable]      # policy name from policies.git_tags
      events: [tag]
    credentials: DOCKER       # → DOCKER_TOKEN or DOCKER_USER + DOCKER_PASS
    retention:
      keep_last: 10
      keep_monthly: 6
```

### Docker README Target

Syncs your README to container registries with badge injection and link rewriting.

```yaml
targets:
  - id: dockerhub-readme
    kind: docker-readme
    url: docker.io
    path: myorg/myapp
    credentials: DOCKER
    file: "README.md"
    description: "Short description for Docker Hub"
    link_base: "https://github.com/myorg/myrepo/blob/main"
```

### Provider Values

| Provider | Registry |
|----------|----------|
| `docker` | Docker Hub |
| `ghcr` | GitHub Container Registry |
| `gitlab` | GitLab Container Registry |
| `quay` | Quay.io |
| `harbor` | Harbor |
| `jfrog` | JFrog Artifactory |
| `gitea` | Gitea Container Registry |
| `generic` | Any OCI registry |

---

## Registry Credentials

The `credentials` field on a registry target sets an environment variable
**prefix**. StageFreight resolves the actual secret from your CI/CD variables
at runtime — nothing sensitive lives in `.stagefreight.yml`.

### Resolution order

For a given prefix (e.g. `HARBOR`), StageFreight checks these suffixes in
order, stopping at the first non-empty value:

| Suffix | Example | Notes |
|--------|---------|-------|
| `_TOKEN` | `HARBOR_TOKEN` | **Preferred.** Scoped, revocable API token. |
| `_PASS` | `HARBOR_PASS` | Accepted. Warning emitted recommending `_TOKEN`. |
| `_PASSWORD` | `HARBOR_PASSWORD` | Accepted. Warning emitted recommending `_TOKEN`. |

Username is always read from `{PREFIX}_USER` (e.g. `HARBOR_USER`).

At the registry protocol level, all three are identical — they become the
`--password-stdin` value passed to `docker login`. The distinction is
entirely on the issuing side:

- **Password** — authenticates the account directly. If it leaks, an
  attacker has account-level access. Rotating it affects every system using
  that account.
- **Token** — a credential issued separately from the account password.
  Tokens can be scoped to specific registries, projects, or actions (e.g.
  push-only, no delete, no admin). They can be revoked individually without
  touching the account or other tokens. Access logs attribute activity to
  the token, not the account, giving a cleaner audit trail.

**Recommendation:** create a robot account or API token in your registry
with the minimum permissions needed (typically: push to the specific
project), store it as `{PREFIX}_TOKEN` in your CI/CD variables, and never
store your account password in CI at all.

### Silencing the warning

If you set `{PREFIX}_PASS` or `{PREFIX}_PASSWORD` but the warning is
noise for your setup (e.g. the value is already a token and you just named
the variable `_PASS`), you can silence it by renaming the CI/CD variable to
use the `_TOKEN` suffix. StageFreight has no way to distinguish a password
from a token by value — the warning is purely based on which suffix was
matched.

### Example — Harbor with a robot account token

```yaml
# .stagefreight.yml
targets:
  - id: harbor-dev
    kind: registry
    build: myapp
    url: cr.example.com
    provider: harbor
    path: myorg/myapp
    tags: ["dev-{sha:8}", "latest-dev"]
    when: { branches: [main], events: [push] }
    credentials: HARBOR
```

```
# GitLab CI/CD variables
HARBOR_USER   =  robot$myorg+stagefreight-push
HARBOR_TOKEN  =  <robot account secret>
```

---

## Build Cache

Controls cache invalidation rules for incremental builds.

```yaml
builds:
  - id: myapp
    kind: docker
    cache:
      auto_detect: true       # detect lockfile changes (default: true)
      watch:
        - paths: ["go.sum"]
          invalidates: ["COPY go.* ./", "RUN go mod download"]
```

---

## Build Strategy Selection

StageFreight selects a build strategy automatically:

| Condition | Strategy | Behavior |
|-----------|----------|----------|
| `--local` flag | **local** | `--load` into daemon, no push |
| Single platform + registries | **load + push** | `--load` then `docker push` each tag |
| Multi-platform + registries | **multi-platform push** | `--push` directly (can't `--load` multi-arch) |
| No registries | **local** | `--load`, default tag `stagefreight:dev` |

---

## Retention Policy

Used by registry and release targets. Policies are additive (restic-style)
— a tag survives if **any** rule wants to keep it.

```yaml
# Shorthand
retention: 10                    # keep last 10

# Full policy
retention:
  keep_last: 3
  keep_daily: 7
  keep_weekly: 4
  keep_monthly: 6
  keep_yearly: 2
  protect: ["latest"]            # never deleted
```

---

## Pattern Syntax

Used by `when.branches`, `when.git_tags`, and all conditional fields.

```yaml
"^main$"              # regex match (default)
"!^feature/.*"        # negated regex (! prefix)
"main"                # literal match
"!develop"            # negated literal
```

Empty list = no filter (always matches). Multiple patterns: evaluated
in order, first match wins.

---

## CLI Commands

See [CLI Reference](reference/CLI.md#cli-stagefreight-docker) for full
flag documentation.

```bash
stagefreight docker build [flags]    # detect → plan → lint → build → push → retention
stagefreight docker readme [flags]   # sync README to container registries
```

---

## Pipeline Phases

During `stagefreight docker build`, these phases run in order:

1. **Lint** — pre-build lint gate (skippable with `--skip-lint`)
2. **Detect** — find Dockerfiles, detect language, resolve context
3. **Plan** — resolve platforms, tags, registries, build strategy
4. **Build** — execute `docker buildx` with layer-parsed output
5. **Push** — push tags to remote registries
6. **Retention** — prune old tags per retention policies

Badges, README sync, and narrator run as separate CI steps:

```
stagefreight badge generate → stagefreight docs generate → stagefreight narrator run → stagefreight docker readme
```
