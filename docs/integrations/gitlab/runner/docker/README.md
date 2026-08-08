# GitLab Runner

Self-hosted GitLab Runner for StageFreight pipelines, packaged as a Docker Compose
stack. Two variants are provided; the only difference is BuildKit and its build cache.

| Stack | Services | Deploy when |
|---|---|---|
| [`gitlab-runner-minimal.yml`](gitlab-runner-minimal.yml) | runner · DinD | the runner never builds images |
| [`gitlab-runner-full.yml`](gitlab-runner-full.yml) | runner · DinD · BuildKit · build cache | the runner builds container images |

The full stack is a superset of the minimal one. Both serve any pipeline that doesn't
build images — GitOps included, which is a matter of [cluster credentials](#cluster-authentication),
not runner shape. Reach for the full stack only when a runner builds images.

## Architecture

Both stacks register a `docker` executor pinned to the host socket
(`--docker-host=unix:///var/run/docker.sock`) on the `stagefreight` Compose network,
where `dind` — and, in the builds stack, `buildkitd` — resolve as service aliases.
Each job is injected with `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH`
(`--env`), so any Docker-consuming job reaches the daemon over verified TLS with no
pipeline-side wiring and runs unprivileged — privileged work is isolated to the DinD
and BuildKit companions.

!!! note "These are the configs we run — adapt them"
    Host paths (`/opt/docker/gitlab-runner/…`), DNS servers, and tokens are
    environment-specific. Substitute your own before deploying.

## Register the runner in GitLab

A runner's identity and policy are set in GitLab at creation, not in the stack.
Create it first — **Settings → CI/CD → Runners → New project runner** — and set:

| Field | Purpose |
|---|---|
| Tags | Pin jobs to the runner — its tags must match a job's `tags:`. |
| Run untagged jobs | Whether the runner accepts jobs that declare no tags. |
| Run on protected branches only | Restrict the runner to protected refs — required for runners holding protected secrets (e.g. an Ansible SSH key) so merge-request pipelines cannot reach them. |

Creation returns a `glrt-…` authentication token — the only secret the stack
requires. Equivalent via the API:

```bash
curl --request POST --header "PRIVATE-TOKEN: <token>" \
  "$CI_SERVER_URL/api/v4/user/runners" \
  --data runner_type=project_type --data project_id=<id> \
  --data description="Build Runner" --data tag_list=my-tag --data run_untagged=false
```

## Deploy

Supply the token and server URL through a `.env` beside the stack (never committed):

```bash
CI_SERVER_URL=https://gitlab.example.com/
RUNNER_TOKEN=glrt-…
RUNNER_NAME=Build Runner
```

Start the stack with `docker compose up -d`, or sync it from a GitOps stack manager —
the registry-mirror configuration is inlined as Compose `configs:`, so no files need
to be staged on the host (requires Docker Compose v2.23+). Registration runs once,
when `config.toml` is absent; to apply changed registration flags, remove
`/opt/docker/gitlab-runner/config/config.toml` and redeploy.

## Cluster authentication

A GitOps pipeline reconciles against a Kubernetes cluster; the runner authenticates
through CI variables only. StageFreight builds a throwaway kubeconfig per job and never
writes a kubeconfig or CA to the host — a GitOps runner is the minimal stack with these
variables set:

| Variable | Purpose |
|---|---|
| `STAGEFREIGHT_OIDC`, or `<CLUSTER>_TOKEN` | Identity — an OIDC token, or a service-account token |
| `<CLUSTER>_CA_B64`, or `<CLUSTER>_CA_FILE` | Cluster CA — base64 PEM, or a path to one |

`<CLUSTER>` is the env prefix derived from the `gitops.cluster` name. Set these as
masked, protected CI variables and declare the cluster in the `gitops:` block — see
[GitOps config](../../../../reference/Config.md#config-gitops).

## Pull-through image cache

Route image pulls through a registry mirror (Harbor, Artifactory, …) with automatic
fallback to the upstream registry. Disabled by default: set a mirror host to enable a
registry; leave it unset and pulls go direct to upstream. Configure per registry in
the stack `.env`:

| Variable | Applies to | Registry |
|---|---|---|
| `REGISTRY_MIRROR_DOCKER` | dockerd + BuildKit | docker.io |
| `REGISTRY_MIRROR_GHCR` | BuildKit | ghcr.io |
| `REGISTRY_MIRROR_QUAY` | BuildKit | quay.io |
| `REGISTRY_MIRROR_LSCR` | BuildKit | lscr.io |

dockerd mirrors Docker Hub only; BuildKit mirrors the other registries for build
bases. Two settings fall outside the stack:

- **Job image.** The image named by a job's `image:` is pulled by the host Docker
  daemon, not DinD. Mirror Docker Hub on the host once:
  ```bash
  echo '{ "registry-mirrors": ["https://docker.example.com"] }' | sudo tee /etc/docker/daemon.json
  sudo systemctl restart docker
  ```
- **Image references.** Reference images by upstream name (`docker.io/…`, `ghcr.io/…`),
  never the mirror hostname — a mirror-hostname reference has no upstream to fall back
  to when the mirror is unavailable.
