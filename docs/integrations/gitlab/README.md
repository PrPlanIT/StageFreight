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

Self-hosted runner as a Docker Compose stack. Two variants; the only difference is BuildKit and
its build cache:

| Stack | Services | Deploy when |
|---|---|---|
| [`gitlab-runner-minimal.yml`](gitlab-runner-minimal.yml) | runner · DinD | the runner never builds images |
| [`gitlab-runner-full.yml`](gitlab-runner-full.yml) | runner · DinD · BuildKit · build cache | the runner builds container images |

The full stack is a superset of the minimal one; reach for it only when a runner builds images.
Both register a `docker` executor pinned to the host socket
(`--docker-host=unix:///var/run/docker.sock`) on the `stagefreight` Compose network, where
`dind` (and `buildkitd`) resolve as service aliases. Each job is injected with `DOCKER_HOST`,
`DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH` (`--env`), so any Docker-consuming job reaches the
daemon over verified TLS and runs unprivileged — privileged work is isolated to the companions.

!!! note "These are the configs we run — adapt them"
    Host paths (`/opt/docker/gitlab-runner/…`), DNS servers, and tokens are environment-specific.

### Register the runner in GitLab

A runner's identity and policy are set in GitLab at creation, not in the stack. Create it first —
**Settings → CI/CD → Runners → New project runner** — and set:

| Field | Purpose |
|---|---|
| Tags | Pin jobs to the runner — its tags must match a job's `tags:`. |
| Run untagged jobs | Whether the runner accepts jobs that declare no tags. |
| Run on protected branches only | Restrict the runner to protected refs — required for runners holding protected secrets (e.g. an Ansible SSH key) so merge-request pipelines cannot reach them. |

Creation returns a `glrt-…` authentication token — the only secret the stack requires. This is
**separate** from the `GITLAB_TOKEN` above: `glrt-…` authorizes the runner to the instance,
`GITLAB_TOKEN` authorizes StageFreight's API calls in the job.

### Deploy

Supply the token and server URL through a `.env` beside the stack (never committed):

```bash
CI_SERVER_URL=https://gitlab.example.com/
RUNNER_TOKEN=glrt-…
RUNNER_NAME=Build Runner
```

Start with `docker compose up -d`, or sync it from a GitOps stack manager — the registry-mirror
config is inlined as Compose `configs:`, so nothing needs to be staged on the host (requires
Docker Compose v2.23+). Registration runs once, when `config.toml` is absent; to apply changed
registration flags, remove `/opt/docker/gitlab-runner/config/config.toml` and redeploy.

### Cluster authentication (GitOps)

A GitOps pipeline reconciles against a Kubernetes cluster; the runner authenticates through CI
variables only. StageFreight builds a throwaway kubeconfig per job and never writes a kubeconfig
or CA to the host — a GitOps runner is the minimal stack with these set:

| Variable | Purpose |
|---|---|
| `STAGEFREIGHT_OIDC`, or `<CLUSTER>_TOKEN` | Identity — an OIDC token, or a service-account token |
| `<CLUSTER>_CA_B64`, or `<CLUSTER>_CA_FILE` | Cluster CA — base64 PEM, or a path to one |

`<CLUSTER>` is the env prefix derived from the `gitops.cluster` name. Set them as masked,
protected CI variables and declare the cluster in the `gitops:` block — see
[GitOps config](../../reference/Config.md#config-gitops).

### Pull-through image cache (optional)

Route image pulls through a registry mirror (Harbor, Artifactory, …) with automatic fallback to
upstream. Disabled by default: set a mirror host to enable a registry; leave it unset and pulls
go direct. Configure per registry in the stack `.env`:

| Variable | Applies to | Registry |
|---|---|---|
| `REGISTRY_MIRROR_DOCKER` | dockerd + BuildKit | docker.io |
| `REGISTRY_MIRROR_GHCR` | BuildKit | ghcr.io |
| `REGISTRY_MIRROR_QUAY` | BuildKit | quay.io |
| `REGISTRY_MIRROR_LSCR` | BuildKit | lscr.io |

dockerd mirrors Docker Hub only; BuildKit mirrors the other registries for build bases. Two
settings fall outside the stack:

- **Job image.** The image named by a job's `image:` is pulled by the host Docker daemon, not
  DinD. Mirror Docker Hub on the host once:
  ```bash
  echo '{ "registry-mirrors": ["https://docker.example.com"] }' | sudo tee /etc/docker/daemon.json
  sudo systemctl restart docker
  ```
- **Image references.** Reference images by upstream name (`docker.io/…`, `ghcr.io/…`), never the
  mirror hostname — a mirror-hostname reference has no upstream to fall back to.

## Example

[`HomeLabHD/ansible`](https://gitlab.prplanit.com/HomeLabHD/ansible) publishes a reusable GitLab
CI component with StageFreight via `kind: gitlab-component`.
