# GitLab

StageFreight renders a native `.gitlab-ci.yml`, drives releases/MRs over the GitLab API, and
can publish a project to the GitLab CI/CD Catalog. The pipeline model (five-job lifecycle,
render, tag gates) is in [Getting Started](../../getting-started.md); this page is the GitLab
specifics.

## Rendering

`stagefreight ci render gitlab --write` writes `.gitlab-ci.yml` — a committed, audition-enforced
artifact marked `DO NOT EDIT`. Live-validated: StageFreight builds itself on GitLab.

## Credentials

`GITLAB_TOKEN` with `api`, `read_repository`, `write_repository` scopes (a **project or group
access token**), set as a masked, protected variable. Without it StageFreight falls back to the
job's built-in `CI_JOB_TOKEN`, which can push to the registry and read artifacts but **cannot
create releases** (no `api` scope).

## CI/CD Catalog components

StageFreight publishes a project to the **GitLab CI/CD Catalog** as a reusable CI component: it
parses the component spec, generates the inputs documentation, registers the version on release,
and links the catalog page from the release notes.

- **Declare the target** — a `gitlab-component` publish target in `.stagefreight.yml`.
  See [Publish](../../config/publish.md).
- **Document the inputs** — generate an inputs table from the spec, or embed it via a stencil so
  it never drifts. See [Stencils & Scribe](../../config/scribe.md).

GitLab-specific constraints:

- The Catalog requires **semver release tags** (`vMAJOR.MINOR.PATCH`); non-semver tags
  (`dev-<sha>`, `latest-dev`) are rejected. Distribute non-semver binaries through a
  [package channel](../../config/package-distribution.md) instead of a release.
- When a `catalog: true` target exists, the release path injects a Catalog link into the notes.
  Disable with `release.catalog_links: false`.

## Runner

A self-hosted runner on [Docker Compose or Kubernetes](runner/README.md): pick a stack,
register it, and deploy.

## Example

[`HomeLabHD/ansible`](https://git.optcp.com/HomeLabHD/ansible) publishes a reusable GitLab
CI component with StageFreight via `kind: gitlab-component`.
