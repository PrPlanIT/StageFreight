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

Self-hosted runner as a Docker Compose stack, or on Kubernetes ([below](#kubernetes-executor)).
Two Compose variants; the only difference is BuildKit and its build cache:

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
The StageFreight image runs as uid 1000 in group 0, so the full stack keeps the cache group-0
writable and the BuildKit client key group-0 readable; a job run as another uid needs the same
access.

DinD keeps every image a job pulls. Bound it in the job env: `SF_DAEMON_GC_MAX_SIZE` (image
store budget) and `SF_DAEMON_GC_MAX_AGE` (evict images not pulled within it). StageFreight then
removes images no container uses, least recently pulled first, never one pulled within
`SF_DAEMON_GC_MIN_AGE` (default `2m`). Unset, images are kept.

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

### Kubernetes executor

StageFreight also runs on GitLab's Kubernetes executor with no Docker daemon: non-root job
pods, a read-only root filesystem, and a buildkitd for image builds. Give the job pods:

| Need | Provide |
|---|---|
| Image builds | A buildkitd the jobs can reach: `BUILDKIT_HOST=tcp://<buildkitd>:1234`, plus its client certs (`ca.pem`, `cert.pem`, `key.pem`) at `/buildkit-certs` (or `BUILDKIT_CERT_PATH`). Builds go through buildx's remote driver. |
| Caches | A ReadWriteMany volume at `/stagefreight` (`SF_CACHE_ROOT=/stagefreight`). It keeps toolchains, Go caches and scanner databases across jobs; without it every job downloads them again. StageFreight's disk GC bounds it, and never evicts a cache a running job is using. |
| Scratch | Writable `HOME`, `/tmp` and build directory, on a volume (a generic ephemeral volume per job keeps job data off the node's disk). |
| Memory | Enough for the audition's test run. The Executor panel reports headroom under the job's own memory limit. |

Without a daemon, everything runs as it does on a Docker runner:
- Images build into the content store and publish promotes, signs and attests them.
- A `kind: command` (or builder-image) step runs inside the job when its image is the CI image, and as a BuildKit build otherwise.
- A crucible build verifies both passes from OCI layouts.
- Ansible converge runs natively when the job image ships `ansible-playbook` and `ssh-agent`; otherwise reconcile is refused at preflight.

Two things need a daemon and are skipped or refused without one: `provider: local` registries,
and Compose `docker_local` targets.

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
  DinD — add the mirror to the host's `/etc/docker/daemon.json`, then restart Docker. Merge this
  key into your existing file; `registry-mirrors` is a list, so if it's already present append to
  it (Docker tries the entries in order, then falls back to Docker Hub):
  ```json
  { "registry-mirrors": ["https://docker.example.com"] }
  ```
  ```bash
  sudo systemctl restart docker
  ```
- **Image references.** Reference images by upstream name (`docker.io/…`, `ghcr.io/…`), never the
  mirror hostname — a mirror-hostname reference has no upstream to fall back to.

## Example

[`HomeLabHD/ansible`](https://git.optcp.com/HomeLabHD/ansible) publishes a reusable GitLab
CI component with StageFreight via `kind: gitlab-component`.
