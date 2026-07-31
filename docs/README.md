# StageFreight

A declarative lifecycle runtime — GitOps, Kubernetes, Docker, and CI ecosystems from one
manifest. StageFreight builds StageFreight: the image, the docs, and the docs site are all
produced by its own pipeline.

> This is the documentation folder. The rendered site lives at
> **[stagefreight.prplanit.com](https://stagefreight.prplanit.com)**; start with
> **[the overview](overview.md)**.

## What it does

- **Detect → Plan → Build** — finds Dockerfiles, resolves tags from git, builds
  multi-platform images via `docker buildx`. → [Builds & Tests](config/builds.md)
- **Multi-Registry Push** — Docker Hub, GHCR, GitLab, Quay, Harbor, JFrog, Gitea, with
  branch/tag routing and digest-preserving promotion. → [Publish](config/publish.md)
- **Cross-Forge Releases** — cut releases on GitLab, GitHub, or Gitea with generated notes,
  badges, and mirror sync. → [Release](config/publish.md#release-cut-forge-releases)
- **Security Scanning** — Trivy + Grype scans and a Syft SBOM, tuned per branch or tag. →
  [Security](config/policy.md#security-scanning)
- **Retention Policies** — restic-style additive retention (`keep_last` / daily / weekly /
  monthly / yearly) across every registry provider. → [Retention](config/concepts.md#retention-policies)
- **Self-Building** — the image, the docs, and the site are produced by StageFreight's own
  pipeline. → [Screenshots](screenshots.md)

## Documentation map

- **[Overview](overview.md)** — the directory and where to start
- **[Integrations](integrations/index.md)** — GitLab, GitHub Actions, Azure DevOps set-up
- **[Configuration](config/index.md)** — the full `.stagefreight.yml` reference
- **[CLI Reference](reference/CLI.md)** — every command and flag
- **[Design](design/index.md)** — pipeline flow, invariants, and boundaries

**There's a setting for every stage — this is theatre.**
