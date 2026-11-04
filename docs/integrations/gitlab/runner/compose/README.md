# GitLab runner on Docker Compose

Two stacks; the only difference is BuildKit and its build cache:

| Stack | Services | Deploy when |
|---|---|---|
| [`minimal.yml`](minimal.yml) | runner · DinD | the runner never builds images |
| [`full.yml`](full.yml) | runner · DinD · BuildKit · build cache | the runner builds container images |

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

## Deploy

[Register the runner](../README.md#register-the-runner-in-gitlab) first, then supply its token
and the server URL through a `.env` beside the stack (never committed):

```bash
CI_SERVER_URL=https://gitlab.example.com/
RUNNER_TOKEN=glrt-…
RUNNER_NAME=Build Runner
```

Start with `docker compose up -d`, or sync it from a GitOps stack manager — the registry-mirror
config is inlined as Compose `configs:`, so nothing needs to be staged on the host (requires
Docker Compose v2.23+). Registration runs once, when `config.toml` is absent; to apply changed
registration flags, remove `/opt/docker/gitlab-runner/config/config.toml` and redeploy.

## Pull-through image cache (optional)

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
