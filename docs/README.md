---
hide:
  - navigation
  - toc
---

<div class="sf-hero" markdown>

![StageFreight](assets/logo.png){ .sf-hero-logo }

# StageFreight

<p class="sf-tagline"><em>The world's a stage, give it a pipeline.</em></p>

A **declarative lifecycle runtime**. One `.stagefreight.yml` drives
**build → sign → release → publish → retain** across GitOps, Kubernetes, Docker, and CI —
one Go binary in place of fragile shell-script pipelines. It builds and ships itself.

[Quick Start](quickstart.md){ .md-button .md-button--primary }
[Documentation Map](overview.md){ .md-button }
[View on GitHub](https://github.com/PrPlanIT/StageFreight){ .md-button }

</div>

<div class="grid cards sf-features" markdown>

-   **Detect → Plan → Build**

    ---

    Finds Dockerfiles, resolves tags from git, builds multi-platform images via `docker
    buildx` — single command, no glue.

    [Builds & Tests →](config/builds.md)

-   **Multi-Registry Push**

    ---

    Docker Hub, GHCR, GitLab, Quay, Harbor, JFrog, Gitea — with branch/tag routing and
    digest-preserving promotion.

    [Targets →](config/targets.md)

-   **Cross-Forge Releases**

    ---

    Cut releases on GitLab, GitHub, or Gitea with generated notes, badges, and mirror sync
    across forges.

    [Targets → Release →](config/targets.md#release-cut-forge-releases)

-   **Security Scanning**

    ---

    Trivy + Grype vulnerability scans and a Syft SBOM, with detail levels tuned per branch
    or tag.

    [Policy → Security →](config/policy.md#security-scanning)

-   **Retention Policies**

    ---

    Restic-style additive retention (`keep_last` / daily / weekly / monthly / yearly) across
    every registry provider.

    [Concepts → Retention →](config/concepts.md#retention-policies)

-   **Self-Building**

    ---

    StageFreight builds StageFreight — the image, the docs, and this very site are produced
    by its own pipeline.

    [Screenshots →](screenshots.md)

</div>

<div class="sf-hero-foot" markdown>

**One file. Every stage. This is theatre.** →
[Browse the full documentation](overview.md)

</div>
