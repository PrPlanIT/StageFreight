# Set-up

StageFreight runs inside GitLab CI and dogfoods itself — every operation happens in a
container. We run **two GitLab runner profiles**, each a small `docker-compose` stack:

| Profile | Purpose | Stack |
|---|---|---|
| [**Build Runner**](gitlab-runner.md) | Builds and everything that isn't GitOps | DinD + BuildKit + the StageFreight cache |
| [**GitOps Runner**](kubernetes.md) | GitOps / Kubernetes pipelines | DinD + docker-executor runner |

Both register a GitLab runner and give StageFreight a Docker daemon (and, for the build
runner, a BuildKit endpoint) to build against. Everything else is declared in your repo's
[`.stagefreight.yml`](../configuration/index.md).

!!! note "These are our real configs — adapt them"
    The compose files on the following pages are the ones we actually run. IPs, DNS servers,
    host paths (`/opt/docker/gitlab-runner/...`), and tokens are environment-specific — swap
    in your own.
