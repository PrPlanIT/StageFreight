# GitLab

StageFreight renders a native `.gitlab-ci.yml`, drives releases/MRs over the GitLab
API, and can publish a project to the GitLab CI/CD Catalog. This page covers the
GitLab-specific pieces; the mechanics live in the linked reference sections.

## Rendering

`stagefreight ci render gitlab --write` generates `.gitlab-ci.yml` (a committed,
audition-enforced artifact). See [CI Integration](../ci.md) for the render/tag/token
details — they are the same across forges.

## CI/CD Catalog components

StageFreight publishes a project to the **GitLab CI/CD Catalog** as a reusable CI
component: it parses the component spec, generates the inputs documentation, registers
the version on release, and links the catalog page from the release notes.

- **Declare the target** — a `gitlab-component` publish target in `.stagefreight.yml`.
  See [Publish](../../config/publish.md).
- **Document the inputs** — generate an inputs table from the spec, or embed it via a
  stencil so it never drifts. See [Stencils & Scribe](../../config/scribe.md).

GitLab-specific constraints:

- The Catalog requires **semver release tags** (`vMAJOR.MINOR.PATCH`); non-semver tags
  (`dev-<sha>`, `latest-dev`) are rejected. Distribute non-semver binaries through a
  [package channel](../../config/package-distribution.md) instead of a release.
- When a `catalog: true` target exists, the release path injects a Catalog link into the
  notes. Disable with `release.catalog_links: false`.

## Runner

Self-hosted runner setup — creating the runner, the compose stacks, and the optional
pull-through cache — is in [GitLab Runner](runner/docker/README.md).

## Example

[`HomeLabHD/ansible`](https://gitlab.prplanit.com/HomeLabHD/ansible) publishes a reusable
GitLab CI component with StageFreight via `kind: gitlab-component`.
