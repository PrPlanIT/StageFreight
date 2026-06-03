# StageFreight — GitLab CI Component (deprecated)

> **Deprecated — do not build on this.** Driving StageFreight via a GitLab CI
> Component is no longer supported and is not developed further. StageFreight
> still *publishes* a component to the GitLab Catalog so the project stays
> discoverable there, but it is a demo stub, not the adoption path.
>
> **Use render instead** — one config, every forge:
>
> ```bash
> stagefreight ci render gitlab --write
> git add .gitlab-ci.yml && git commit
> ```
>
> Why deprecated: a driver-component's `inputs` are a 1:1, hand-maintained replica
> of `.stagefreight.yml` that drifts on every change, and the component format is
> GitLab-specific — both against StageFreight's one-config-many-forges design.
> See [`templates/README.md`](../templates/README.md) and the
> [integrations matrix](../integrations/README.md).

## Inputs (published demo component)

Retained for Catalog discoverability only; the inputs below are not a supported
configuration surface.

<!-- sf:component:start -->
## `stagefreight`

### Ungrouped
| Name | Required | Default | Description |
|------|----------|---------|-------------|
| `stagefreight_image` | ❌ | `docker.io/prplanit/stagefreight:latest-dev` | StageFreight image to use for the build |
| `dind_image` | ❌ | `docker.io/docker:27-dind` | Docker-in-Docker service image |
| `stagefreight_args` | ❌ | - | Additional arguments passed to stagefreight docker build |
| `security_scan` | ❌ | `true` | Run security scan after build |
| `security_detail` | ❌ | `counts` | Security detail level for release notes (none, counts, detailed, full) |
| `release_enabled` | ❌ | `true` | Create a release on the forge after build |
<!-- sf:component:end -->
