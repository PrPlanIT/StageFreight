# GitOps Runner

Our runner for **GitOps / Kubernetes** pipelines (`lifecycle.mode: gitops`) — it's where we
run Flux manifest validation and reconciliation. It's a simpler stack than the
[Build Runner](gitlab-runner-builds.md): **DinD** + the **GitLab runner** with the docker executor,
and no BuildKit (GitOps repos don't build images). Jobs run on the **host docker**
(socket executor) and join the `stagefreight` compose network, where `dind` is a network
alias — so the perform phase's docker work (StageFreight's containerized **ansible
converge** runs plays inside an execution image) reaches `tcp://dind:2376` with verified
TLS out of the box.

```yaml
# GitLab runner stack on dungeon-pedestal — the gitops runner (k8s-dungeon-production).
# Freightyard architecture: jobs run on the HOST docker (socket executor) and join the
# stagefreight compose network, where `dind` is a network alias — so job containers
# resolve tcp://dind:2376 natively and mount the dind client certs by host path.
# Jobs receive DOCKER_HOST/DOCKER_TLS_VERIFY/DOCKER_CERT_PATH as runner-provided env
# (--env), so ANY project's docker-consuming jobs work here — no pipeline-side wiring
# required. The runner's OWN executor connection is pinned to the host socket
# (--docker-host), which makes that job env injection safe: an explicit host cannot be
# overridden by environment lookup. Jobs run
# unprivileged (docker work happens inside the dind companion, not the job container).
#
# Registration only runs when /etc/gitlab-runner/config.toml is absent — flag changes
# here need a fresh register (remove the generated config.toml) on redeploy.
# Secrets via stack environment: CI_SERVER_URL, RUNNER_TOKEN (glrt-… authentication
# token — the runner is CREATED in GitLab first, where tags/untagged/locked live).

services:
  dind:
    image: docker:dind
    privileged: true
    restart: always
    environment:
      DOCKER_TLS_CERTDIR: /certs
      DOCKER_TLS_SAN: DNS:dind
    volumes:
      - dind-storage:/var/lib/docker
      - /opt/docker/gitlab-runner/certs:/certs
    healthcheck:
      test: ["CMD", "docker", "info"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 5s
    networks:
      stagefreight:
        aliases:
          - dind

  register-runner:
    image: gitlab/gitlab-runner:latest
    restart: 'no'
    depends_on:
      dind:
        condition: service_healthy
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        if [ ! -f /etc/gitlab-runner/config.toml ]; then
          echo "Config does not exist, registering runner..."
          gitlab-runner register \
            --docker-dns=10.0.0.1 \
            --docker-dns=10.0.0.2 \
            --docker-dns=172.22.30.122 \
            --docker-dns=1.1.1.1 \
            --docker-dns=8.8.8.8 \
            --docker-host=unix:///var/run/docker.sock \
            --docker-image=alpine:latest \
            --docker-network-mode=stagefreight \
            --docker-privileged=false \
            --docker-volumes=/cache \
            --docker-volumes=/opt/docker/gitlab-runner/certs/client:/certs/client:ro \
            --env=DOCKER_CERT_PATH=/certs/client \
            --env=DOCKER_HOST=tcp://dind:2376 \
            --env=DOCKER_TLS_VERIFY=1 \
            --executor=docker \
            --name=$${RUNNER_NAME:-"GitLab Runner"} \
            --non-interactive \
            --token=$${RUNNER_TOKEN} \
            --url=$${CI_SERVER_URL}
        else
          echo "Runner config already exists, skipping registration"
        fi
    environment:
      - CI_SERVER_URL=${CI_SERVER_URL}
      - RUNNER_TOKEN=${RUNNER_TOKEN}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/docker/gitlab-runner/config:/etc/gitlab-runner:z
    networks:
      - stagefreight

  runner:
    image: gitlab/gitlab-runner:latest
    restart: always
    depends_on:
      dind:
        condition: service_healthy
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/docker/gitlab-runner/config:/etc/gitlab-runner:z
    healthcheck:
      test: ["CMD", "gitlab-runner", "list"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    networks:
      - stagefreight

networks:
  stagefreight:
    name: stagefreight

volumes:
  dind-storage:
```

The stack reads its secrets from a `.env` beside the compose file (never committed):

```bash
CI_SERVER_URL=https://gitlab.example.com/
RUNNER_TOKEN=glrt-…             # authentication token from the runner created in GitLab
RUNNER_NAME=Dungeon-Pedestal    # description shown in GitLab; identity itself lives in GitLab
```

!!! note "Cluster authentication"
    In GitOps mode StageFreight validates and reconciles against a Kubernetes cluster, so the
    runner needs credentials to reach it — in our setup, a CA used for OIDC-style auth. That
    cluster-auth material is configured per-cluster and layered on top of this runner; it's
    not part of the compose stack above.

## Runner identity

This runner's identity is defined **in GitLab at creation** (token flow — see
[Build Runner § Runner identity lives in GitLab](gitlab-runner-builds.md#runner-identity-lives-in-gitlab)):

| Runner | Tags | Run untagged |
|---|---|---|
| **Dungeon-Pedestal** | `k8s-dungeon-production` | no |
