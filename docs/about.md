# About StageFreight

StageFreight is a **declarative lifecycle runtime**: one `.stagefreight.yml` at the repo root
drives **build → sign → release → publish → retain** across GitOps, Kubernetes, Docker, and
CI. It replaces the pile of bespoke shell scripts a project accretes with a single Go binary
that speaks each forge and registry natively — and it builds, scans, documents, and releases
itself.

> *The world's a stage, give it a pipeline.*

## What it does

- **Detect → plan → build** multi-platform images via `docker buildx`, plus binaries and
  container-run outputs.
- **Push to any registry** — Docker Hub, GHCR, GitLab, Quay, Harbor, JFrog, Gitea — with
  branch/tag routing and digest-preserving promotion.
- **Cut cross-forge releases** on GitLab, GitHub, or Gitea with generated notes, badges, and
  mirror sync.
- **Scan** images with Trivy + Grype and emit a Syft SBOM.
- **Lint** with cache-aware, parallel modules that scan only what changed.
- **Retain** artifacts with restic-style additive policies across every provider.
- **Render its own CI** natively per forge, and **narrate** badges, shields, and generated
  docs back into the repository.

## Where to go next

- New here → [Quick Start](quickstart.md).
- Configure it → [Configuration](config/index.md).
- See it run → [Screenshots](screenshots.md).
- Why it's built this way → [Philosophy](philosophy.md).

## License

Distributed under [AGPL-3.0-only](licensing.md). Commercial licenses are available for
organizations that need alternative terms.
