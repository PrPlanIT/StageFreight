# Build Runner

Our runner for **builds and everything that isn't GitOps**. It runs on a shared
`stagefreight` docker network with three long-lived services plus a one-shot registration:

- **DinD** — the Docker daemon builds talk to, exposing a TLS endpoint at `tcp://dind:2376`.
  Job containers receive `DOCKER_HOST` + `DOCKER_TLS_VERIFY` + the client certs.
- **BuildKit** (`buildkitd`) — for buildx builds. It generates its **own independent PKI**
  (CA / server / client certs) on first boot — separate from DinD — and serves
  `tcp://buildkitd:1234` with `--oci-worker-gc` storage limits.
- **GitLab runner** — docker executor, joined to the `stagefreight` network.
- The **StageFreight cache** is a host volume (`/opt/docker/gitlab-runner/stagefreight`)
  mounted into DinD, BuildKit, and job containers at `/stagefreight`.

!!! tip "Why BuildKit gets its own PKI"
    The `buildkitd-certs` init container mints a CA and server/client certs scoped to
    `DNS:buildkitd` so BuildKit's TLS is fully independent of DinD's — the two daemons don't
    share trust.

```yaml
# GitLab runner stack on freightyard — the build runner (StageFreight crucible + governance).
# Jobs run on the HOST docker (socket executor) and join the stagefreight compose network,
# where `dind` and `buildkitd` are network aliases — job containers resolve
# tcp://dind:2376 natively and mount the dind client certs by host path. BuildKit runs
# beside dind with its own independent PKI (generated once by buildkitd-certs); jobs mount
# its client certs at /buildkit-certs. The /stagefreight host dir is the shared SF cache
# root (toolchains, buildx state), mounted into dind, buildkitd, and every job.
# Jobs receive DOCKER_HOST/DOCKER_TLS_VERIFY/DOCKER_CERT_PATH as runner-provided env
# (--env), so ANY project's docker-consuming jobs work here — no pipeline-side wiring
# required. The runner's OWN executor connection is pinned to the host socket
# (--docker-host), which makes that job env injection safe: an explicit host cannot be
# overridden by environment lookup. Jobs run
# unprivileged (docker work happens inside the companions, not the job container).
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
      - /opt/docker/gitlab-runner/stagefreight:/stagefreight
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

  buildkitd-certs:
    image: docker.io/library/alpine:3
    restart: "no"
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        if [ -f /out/server/cert.pem ]; then
          echo "BuildKit PKI already exists, skipping"
          exit 0
        fi
        apk add --no-cache openssl >/dev/null 2>&1
        echo "Generating independent BuildKit PKI..."
        mkdir -p /out/ca /out/server /out/client

        # CA
        openssl genrsa -out /out/ca/key.pem 4096 2>/dev/null
        openssl req -new -x509 -key /out/ca/key.pem -out /out/ca/cert.pem \
          -days 3650 -subj "/CN=BuildKit CA" 2>/dev/null

        # Server cert (SAN: DNS:buildkitd)
        openssl genrsa -out /out/server/key.pem 4096 2>/dev/null
        openssl req -new -key /out/server/key.pem -out /tmp/server.csr \
          -subj "/CN=buildkitd" 2>/dev/null
        printf "[v3]\nsubjectAltName=DNS:buildkitd,DNS:localhost,IP:127.0.0.1" > /tmp/ext.cnf
        openssl x509 -req -in /tmp/server.csr \
          -CA /out/ca/cert.pem -CAkey /out/ca/key.pem -CAcreateserial \
          -out /out/server/cert.pem -days 3650 \
          -extensions v3 -extfile /tmp/ext.cnf 2>/dev/null

        # Client cert
        openssl genrsa -out /out/client/key.pem 4096 2>/dev/null
        openssl req -new -key /out/client/key.pem -out /tmp/client.csr \
          -subj "/CN=buildkit-client" 2>/dev/null
        openssl x509 -req -in /tmp/client.csr \
          -CA /out/ca/cert.pem -CAkey /out/ca/key.pem -CAcreateserial \
          -out /out/client/cert.pem -days 3650 2>/dev/null
        cp /out/ca/cert.pem /out/client/ca.pem

        echo "BuildKit PKI generated (independent from DinD)"
    volumes:
      - /opt/docker/gitlab-runner/buildkitd-certs:/out

  buildkitd:
    image: moby/buildkit:latest
    privileged: true
    restart: always
    depends_on:
      buildkitd-certs:
        condition: service_completed_successfully
    command:
      - --addr
      - tcp://0.0.0.0:1234
      - --tlscacert=/certs/ca/cert.pem
      - --tlscert=/certs/server/cert.pem
      - --tlskey=/certs/server/key.pem
      - --oci-worker-gc=true
      - --oci-worker-gc-keepstorage=12000
    volumes:
      - buildkitd-state:/var/lib/buildkit
      - /opt/docker/gitlab-runner/stagefreight:/stagefreight
      - /opt/docker/gitlab-runner/buildkitd-certs:/certs:ro
    healthcheck:
      test: ["CMD-SHELL", "buildctl --addr tcp://127.0.0.1:1234 --tlscacert /certs/ca/cert.pem --tlscert /certs/client/cert.pem --tlskey /certs/client/key.pem debug workers"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s
    networks:
      stagefreight:
        aliases:
          - buildkitd

  register-runner:
    image: gitlab/gitlab-runner:latest
    restart: "no"
    depends_on:
      dind:
        condition: service_healthy
      buildkitd:
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
            --docker-volumes=/opt/docker/gitlab-runner/stagefreight:/stagefreight \
            --docker-volumes=/opt/docker/gitlab-runner/buildkitd-certs/client:/buildkit-certs:ro \
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
      buildkitd:
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
  buildkitd-state:
```

The stack reads its secrets from a `.env` beside the compose file (never committed):

```bash
CI_SERVER_URL=https://gitlab.example.com/
RUNNER_TOKEN=glrt-…        # authentication token from the runner created in GitLab
RUNNER_NAME=Freightyard    # description shown in GitLab; identity itself lives in GitLab
```

## Runner identity lives in GitLab

Both runners register with a **`glrt-…` authentication token** (`--token`): create the
runner in GitLab first (project → Settings → CI/CD → Runners → *New project runner*) and
pick its **name, tags, run-untagged policy, and locked status** there. The compose stack
only needs the resulting token in its `.env` — nothing in the stack sets identity.

You can also create the runner via the API, which hands back the `glrt-…` token for the
`.env` in one shot:

```bash
curl --request POST --header "PRIVATE-TOKEN: <token>" \
  "https://gitlab.example.com/api/v4/user/runners" \
  --data runner_type=project_type \
  --data project_id=<id> \
  --data description=Dungeon-Pedestal \
  --data tag_list=k8s-dungeon-production \
  --data run_untagged=false
```

Tags are how you pin projects to a specific runner: give the runner a tag, enable it for
the project, and tag the project's jobs to match — same mechanism for any project, no
stack changes.

Our runner records:

| Runner | Tags | Run untagged |
|---|---|---|
| **Freightyard** (this page) | `sf-governance-prplanit`, `sf-build-internal` | yes |
| **[Dungeon-Pedestal](gitlab-runner-gitops-k8s.md)** | `k8s-dungeon-production` | no |

!!! warning "Registration is one-shot"
    `register-runner` only runs when the generated `config.toml` is **absent**. To apply
    changed registration flags (or bind to a newly created runner), remove
    `/opt/docker/gitlab-runner/config/config.toml` and redeploy — otherwise the stack keeps
    the old registration silently.
