# Azure DevOps

> **Status: experimental.** Render and the forge client are implemented and unit-tested,
> but not yet validated against a live Azure DevOps run — see the checklist below.

StageFreight renders a native `azure-pipelines.yml` and drives refs/commits/tags/PRs over
the Azure DevOps API. Azure has no native git-release object, so release surfaces return
`ErrNotSupported` by design (use tags).

## Rendering

`stagefreight ci render azuredevops --write` writes `azure-pipelines.yml`. Render, tags,
and credentials are covered in [CI Integration](../ci.md); only the Azure-specific pieces
are below.

## Agent

[`k8s/stagefreight-azure-agent.yaml`](k8s/stagefreight-azure-agent.yaml) — a self-hosted
Azure DevOps agent in Kubernetes with `buildkitd` (image builds) and `dind`, the k8s
analog of the GitLab [`gitlab-runner-full.yml`](../gitlab/runner/docker/gitlab-runner-full.yml)
stack. The three containers share one pod, so the builders are reached on `127.0.0.1`
rather than the service names GitLab uses:

| Component | Access |
|---|---|
| agent | runs jobs (the StageFreight CI image as the `container:` job) |
| build jobs | `BUILDKIT_HOST=tcp://127.0.0.1:1234` — independent mTLS PKI |
| docker jobs | `DOCKER_HOST=tcp://127.0.0.1:2376` — DinD TLS |
| other jobs | no daemon access |

## Setup

1. **Create an agent pool** — *Project settings → Agent pools → Add pool* (self-hosted).
2. **Create a PAT** with **Agent Pools (read & manage)**.
3. **Build an agent image** per Microsoft's
   [self-hosted agent in Docker](https://learn.microsoft.com/azure/devops/pipelines/agents/docker)
   guide and push it to a registry the cluster can pull.
4. **Edit** `k8s/stagefreight-azure-agent.yaml` — set `AZP_URL`, `AZP_TOKEN`, `AZP_POOL`,
   and the agent image (`REPLACE_WITH_AZP_AGENT_IMAGE`).
5. **Apply** — `kubectl apply -f k8s/stagefreight-azure-agent.yaml`.
6. Confirm the agent shows **Online** in the pool, then run a pipeline with
   `pool: { name: <your pool> }`.

## Credentials

Two separate tokens — don't conflate them:

| Token | For | Scope |
|---|---|---|
| `AZP_TOKEN` (agent env) | registering the agent with the pool | Agent Pools (read & manage) |
| `SYSTEM_ACCESSTOKEN`, or `AZURE_DEVOPS_TOKEN` (job env) | StageFreight's git ops — refs, commits, tags, PRs | Code (read & write) |

`SYSTEM_ACCESSTOKEN` is the pipeline's built-in token, but Azure does **not** expose it to
jobs by default — map it in and grant the build service Contribute:

```yaml
    env:
      SYSTEM_ACCESSTOKEN: $(System.AccessToken)
```

Org/project/repo auto-detect from `SYSTEM_COLLECTIONURI` / `SYSTEM_TEAMPROJECT` /
`BUILD_REPOSITORY_NAME`. See [CI Integration → Credentials](../ci.md#credentials).

## Validation checklist

The topology is proven on GitLab; these Azure-specific points still need a live run to
graduate from experimental:

- [ ] Agent registers and shows Online.
- [ ] A `container: ci` job reaches `127.0.0.1:1234` (buildkitd) and `:2376` (dind). If
      Azure isolates the container job's network, drop `container:` and run StageFreight
      directly on the agent, or wire host networking — the main thing to verify.
- [ ] DinD has generated `/certs/client` before the first build (init ordering).
- [ ] `stagefreight ci run perform` builds via buildkitd (mTLS handshake succeeds).
- [ ] `SYSTEM_ACCESSTOKEN` is mapped into the job env (or `AZURE_DEVOPS_TOKEN` is set) and
      the build service has Contribute.
- [ ] Forge-client ops work against the real org (auth, refs, commits, tags, PRs).

Record what you change so the next person doesn't rediscover it — that's what moves Azure
from experimental to supported.
