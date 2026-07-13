# `.stagefreight.yml` Example Manifests

Example configurations for every project archetype. Each file is a standalone, copy-paste-ready `.stagefreight.yml` using the **v1 schema** with comments explaining who it's for, what it does, and why.

Features that are planned but not yet implemented are preserved as commented `# ── Roadmap` blocks at the bottom of each file.

## Per-Repo Manifests (`.stagefreight.yml`)

| # | Example | Who it's for |
|---|---------|-------------|
| 01 | [minimal](01-minimal.yml) | Solo dev, hobby project — build + push, nothing else |
| 02 | [personal-docker-app](02-personal-docker-app.yml) | Solo dev shipping a containerized side project to Docker Hub + GHCR |
| 03 | [patched-fork](03-patched-fork.yml) | Anyone maintaining local patches on top of upstream — `-patched` tags |
| 04 | [mirror](04-mirror.yml) | Read-only mirror placeholder — declaration for future daemon support |
| 05 | [multi-image](05-multi-image.yml) | Monolithic codebase producing app + worker + migrations from one Dockerfile |
| 06 | [cross-platform-cli](06-cross-platform-cli.yml) | CLI binary — release notes now, artifact builds when the feature ships |
| 07 | [multi-toolchain-build](07-multi-toolchain-build.yml) | Multi-SDK project — release notes + security now, artifact builds later |
| 08 | [full-lifecycle](08-full-lifecycle.yml) | Business app — every implemented v1 feature turned on |
| 09 | [internal-tool](09-internal-tool.yml) | Private team tooling — single registry, all branches push |
| 10 | [library-package](10-library-package.yml) | npm/PyPI/Go library — release notes + security scanning, no Docker |
| 11 | [static-site](11-static-site.yml) | Documentation repo — release notes + security scanning, no Docker |
| 12 | [homelab-fork](12-homelab-fork.yml) | Homelab user with a customized fork — build + push with `-custom` tags |
| 13 | [compliance-strict](13-compliance-strict.yml) | Regulated industry — full lint + security, SBOM required, fail_on_critical |
| 14 | [microservices-monorepo](14-microservices-monorepo.yml) | Multiple distinct services in one repo, each with own Dockerfile |
| 15 | [headless-worker](15-headless-worker.yml) | Queue consumer / bot / cron job — dual-target (public + private branch builds) |
| 16 | [helm-chart](16-helm-chart.yml) | Helm chart or IaC module — release notes + security scanning |
| 17 | [game-server](17-game-server.yml) | Modded game server — build + push to private registry on every push |
| 18 | [mobile-hybrid-app](18-mobile-hybrid-app.yml) | Mobile/hybrid app — container build for API backend, mobile artifacts later |
| 19 | [open-source-maintainer](19-open-source-maintainer.yml) | OSS project — Docker Hub + GHCR + Quay, README sync, SBOM |
| 20 | [data-pipeline](20-data-pipeline.yml) | ETL / data pipeline — branch builds to private registry |
| 21 | [ml-model](21-ml-model.yml) | ML model serving — GPU image with CUDA build args, dual registry |
| 22 | [s6-overlay-linuxserver](22-s6-overlay-linuxserver.yml) | S6-overlay / LinuxServer.io style — real LSCR tag patterns, 3 channels |
| 23 | [ansible-collection](23-ansible-collection.yml) | Ansible collection/role — release notes + security scanning |

## Daemon Configuration

| # | Example | What it is |
|---|---------|-----------|
| 24 | [daemon-config](24-daemon-config.yml) | `stagefreight serve` config — providers, org defaults, mirrors, alerts |

## Manifest Complexity Gradient (implemented v1 features only)

```
~3 lines   04-mirror                        ← placeholder declaration
~10 lines  01-minimal                       ← build + push, nothing else
~15 lines  06-cli, 07-multi-toolchain,      ← release + security, no builds
           10-library, 11-static, 16-helm,
           23-ansible
~30 lines  02-personal, 09-internal,        ← single build + a few targets
           12-homelab, 17-game, 18-mobile
~50 lines  03-fork, 15-worker, 20-data,     ← build + multi-target
           21-ml-model
~80 lines  05-multi-image, 14-monorepo      ← multi-build + multi-target
~100 lines 08-full, 13-compliance,          ← builds + targets + lint + security
           19-oss, 22-s6-overlay
```
