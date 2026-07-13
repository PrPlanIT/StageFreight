# Package Distribution (`kind: generic-package`)

Registry-style binary distribution for automation consumers. A `generic-package`
target publishes your build archives to a forge's **generic package registry**,
where they can be pulled by `curl` (tokenless on public projects) under a stable,
predictable URL — without going through forge *releases*.

This is the first backend of StageFreight's registry-distribution model. It shares
archive resolution, repo/forge resolution, and the retention engine with
`kind: release`, but it has its own identity model: a package has a **version**,
not a git tag.

## When to use this vs `kind: release`

| Use `kind: release` when… | Use `kind: generic-package` when… |
|---|---|
| You want a human-facing release page with notes, checksums, and a "latest release" affordance | You want a machine-oriented, curl-pullable artifact under a stable registry URL |
| The project is a GitLab **CI/CD Catalog component** (releases must be semver) and you still want per-push dev binaries — releases can't take `dev-{sha8}` there | Per-push dev binaries on a catalog project — packages aren't releases, so no semver/catalog coupling |
| Distributing to **GitHub** (no generic package registry) | Distributing to **GitLab** (or Gitea/Forgejo, once wired) |

The two are not exclusive — a project can publish releases *and* packages.

## Configuration

```yaml
targets:
  - id: app-pkg-dev
    kind: generic-package
    repo: primary                 # repos[].id — forge + project (identity comes from here)
    package: app                  # package name (default: the project's basename)
    archives: app-dev-archive     # references a kind: binary-archive target
    version: "dev-{sha:8}"        # immutable package version (REQUIRED)
    aliases: ["latest-dev"]       # rolling versions, overwritten every publish (optional)
    retention: { keep_last: 6, protect: ["latest-dev"] }
    when: { branches: [main], events: [push] }
```

Fields:

- **`repo`** *(required)* — references a `repos[]` entry; the forge, base URL,
  project, and credentials are resolved from it. The package is published to that
  forge's generic package registry.
- **`package`** — the package name. Defaults to the project's basename.
- **`archives`** *(required)* — the `kind: binary-archive` target whose built
  archives are published as the package files.
- **`version`** *(required)* — the **immutable** version pattern (e.g.
  `dev-{sha:8}` → `dev-abc12345`). This is a package *version*, deliberately **not**
  a release `tag:` — a package's identity is its version. Required because every
  rolling alias must have an immutable version behind it (alias-only publication is
  rejected at config-load).
- **`aliases`** — rolling versions (e.g. `latest-dev`) refreshed on every publish.
- **`retention`** — restic-style policy; see below.
- **`when`** — the usual event/branch/tag routing.

## Immutable vs mutable versions

This distinction is a contract — automation can depend on it:

| Version type   | Behavior                          |
| -------------- | --------------------------------- |
| `dev-abc12345` | **Immutable** — published once, never replaced. Re-running the pipeline on the same commit does not re-publish it. |
| `latest-dev`   | **Mutable** — refreshed every publish (delete-then-publish), always points at the newest build. |

## Pulling

The pull URL is the version path itself — stable and tokenless on public projects:

```bash
# Newest dev build (rolling alias)
curl -L "https://gitlab.example.com/api/v4/projects/<id>/packages/generic/app/latest-dev/app-linux-amd64.tar.gz" \
  -o app.tar.gz

# A specific immutable build
curl -L "https://gitlab.example.com/api/v4/projects/<id>/packages/generic/app/dev-abc12345/app-linux-amd64.tar.gz" \
  -o app.tar.gz
```

`<id>` is the numeric project id or the URL-encoded `group%2Fproject` path. On a
private project, authenticate with a header (`--header "PRIVATE-TOKEN: <token>"`)
or `?private_token=<token>`.

## Retention

Retention prunes the **immutable version family** derived from your `version`
template (e.g. `dev-{sha:8}` → everything matching `^dev-.+$`), keeping the newest
per the policy. Rolling **aliases are always protected** — `latest-dev` is never
pruned (it's also folded into the protect set automatically). Policies are the same
restic-style additive rules used everywhere else (`keep_last`, `keep_daily`, …,
`protect`).

```yaml
retention: { keep_last: 6, protect: ["latest-dev"] }
```

## Forge support

| Forge | Generic packages | Notes |
|---|---|---|
| **GitLab** | ✓ | Full support (publish / list / prune). |
| **Gitea / Forgejo** | deferred | They have a generic package API; not yet wired (returns an explicit error). |
| **GitHub** | ✗ | No generic package registry (Packages is typed-only). Use `kind: release` for binary distribution. |
| **Azure DevOps** | ✗ | Not supported. |

A `generic-package` target pointed at an unsupported forge fails fast with a clear
message rather than silently doing nothing.
