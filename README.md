<p align="center">
  <img src="src/assets/logo.png" width="220" alt="StageFreight">
</p>

# StageFreight

> *The world's a stage, give it a pipeline.*

A declarative lifecycle runtime that governs Git as the source of truth, enforcing operator-defined intent across GitOps workflows, Kubernetes, Docker, and CI ecosystems. StageFreight is open-source, self-building, and replaces fragile shell-script CI pipelines with a single Go binary driven by one [`.stagefreight.yml`](.stagefreight.yml) file. There's a setting for every stage — this is theatre.

<!-- sf:project:start -->
[![GitHub](https://img.shields.io/badge/GitHub-source-181717?logo=github)](https://github.com/PrPlanIT/StageFreight) [![GitLab](https://img.shields.io/badge/GitLab-source-FC6D26?logo=gitlab)](https://gitlab.prplanit.com/PrPlanIT/stagefreight) [![Go Report Card](https://goreportcard.com/badge/github.com/PrPlanIT/StageFreight)](https://goreportcard.com/report/github.com/PrPlanIT/StageFreight) [![Go Reference](https://pkg.go.dev/badge/github.com/PrPlanIT/StageFreight.svg)](https://pkg.go.dev/github.com/PrPlanIT/StageFreight) [![Last Commit](https://img.shields.io/github/last-commit/PrPlanIT/StageFreight)](https://github.com/PrPlanIT/StageFreight/commits) [![Open Issues](https://img.shields.io/github/issues/PrPlanIT/StageFreight)](https://github.com/PrPlanIT/StageFreight/issues) [![Open PRs](https://img.shields.io/github/issues-pr/PrPlanIT/StageFreight)](https://github.com/PrPlanIT/StageFreight/pulls) [![Contributors](https://img.shields.io/github/contributors/PrPlanIT/StageFreight)](https://github.com/PrPlanIT/StageFreight/graphs/contributors)
<!-- sf:project:end -->
<!-- sf:badges:start -->
[![build](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/build.svg)](https://gitlab.prplanit.com/PrPlanIT/stagefreight/-/pipelines) [![license](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/license.svg)](https://github.com/PrPlanIT/StageFreight/blob/main/LICENSE) [![release](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/release.svg)](https://github.com/PrPlanIT/StageFreight/releases) ![updated](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/updated.svg) [![donate](https://img.shields.io/badge/donate-FF5E5B?logo=ko-fi&logoColor=white)](https://ko-fi.com/T6T41IT163) [![sponsor](https://img.shields.io/badge/sponsor-EA4AAA?logo=githubsponsors&logoColor=white)](https://github.com/sponsors/PrPlanIT)
<!-- sf:badges:end -->
<!-- sf:image:start -->
[![Docker](https://img.shields.io/badge/Docker-prplanit%2Fstagefreight-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/prplanit/stagefreight) [![pulls](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/pulls.svg)](https://hub.docker.com/r/prplanit/stagefreight)

[![latest](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/release-latest.svg)](https://hub.docker.com/r/prplanit/stagefreight/tags?name=latest) ![updated](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/release-updated.svg) [![size](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/release-size.svg)](https://hub.docker.com/r/prplanit/stagefreight/tags?name=v0.7.0) [![latest-dev](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/dev-latest.svg)](https://hub.docker.com/r/prplanit/stagefreight/tags?name=latest-dev) ![updated](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/dev-updated.svg) [![size](https://raw.githubusercontent.com/PrPlanIT/StageFreight/main/.stagefreight/scribe/dev-size.svg)](https://hub.docker.com/r/prplanit/stagefreight/tags?name=latest-dev)
<!-- sf:image:end -->

---

## The Canonical Pipeline

You describe intent once in [`.stagefreight.yml`](.stagefreight.yml), and StageFreight runs it as a **lifecycle** — the same five stages whether it's shipping a container image, cutting a cross-forge release, or reconciling a cluster. It even renders your forge's pipeline file *for you* — `stagefreight ci render gitlab` writes the `.gitlab-ci.yml` (also GitHub, Gitea, Forgejo) — so the CI document is a **generated artifact StageFreight owns, never hand-maintained.**

| Act | What it does | Powered by |
| --- | --- | --- |
| **Audition** | Prove the change before anything acts on it | lint · tests · policy · config-freshness |
| **Perform** | The one authoritative build — or, per mode, a reconcile | docker image · binary · gitops · compose |
| **Review** | Scan what was built, before it can ship | Trivy · Grype · SBOM |
| **Publish** | The *only* act that distributes | registries · releases · packages · pages · project metadata |
| **Narrate** | The report that always runs, even on failure | badges · notifications · run summary |

Git is the source of truth: the acts only enact **accepted** state — never a merge request, never an unmerged branch. The same binary is both the CI image and your local CLI.

### Features:

|                                |                                                                                                           |
| ------------------------------ | --------------------------------------------------------------------------------------------------------- |
| **Detect → Plan → Build**      | Finds Dockerfiles, resolves tags from git, builds multi-platform images via `docker buildx`               |
| **Multi-Registry Push**        | Docker Hub, GHCR, GitLab, Quay, Harbor, JFrog, Gitea — with branch/tag filtering via regex (`!` negation) |
| **Security Scanning**          | Trivy + Grype vulnerability scan, Syft SBOM generation, configurable detail levels per branch or tag      |
| **Cross-Forge Releases**       | Create releases on GitLab, GitHub, or Gitea with auto-generated notes, badges, and cross-platform sync    |
| **Cache-Aware Linting**        | Parallel lint modules, delta-only on changed files, with JUnit reporting for CI                           |
| **Retention Policies**         | Restic-style tag retention (keep_last, daily, weekly, monthly, yearly) across all registry providers       |
| **Self-Building**              | StageFreight builds, scans, and releases itself through its own pipeline — this image is one of its own artifacts |

### Documentation:

|                     |                                                                 |
| ------------------- | --------------------------------------------------------------- |
| Start Here          | [Quick-Start Scenarios](docs/quickstart.md) · [Full Docs](https://stagefreight.prplanit.com) |
| CLI Reference       | [Full Command Reference](docs/reference/CLI.md)                |
| Config Reference    | [Full Config Schema](docs/reference/Config.md)                 |
| Manifest Examples   | [Aspirational Example Configs](docs/config/aspirational/) · [Quick Examples](docs/config/examples/) |
| Roadmap             | [Full Vision](docs/design/plans/RoadMap.md)              |
| GitLab Components | [Publishing GitLab Components](docs/integrations/gitlab/GitLab-Components.md) |

---

## Image Contents

#### Base Images
<!-- sf:contents-base:start -->
![alpine](https://img.shields.io/badge/alpine-3.24.1-0078D4?style=flat) ![golang](https://img.shields.io/badge/golang-1.26.5-0078D4?style=flat)
<!-- sf:contents-base:end -->

#### Runtime Packages
<!-- sf:contents-apk:start -->
![chafa](https://img.shields.io/badge/chafa-555?style=flat) ![git](https://img.shields.io/badge/git-555?style=flat) ![tree](https://img.shields.io/badge/tree-555?style=flat)
<!-- sf:contents-apk:end -->

---

## Contributing

- Fork the repository
- Submit Pull Requests / Merge Requests
- Open issues with ideas, bugs, or feature requests

## Disclaimer

The Software provided hereunder is licensed "as-is," without warranties of any kind. The developer makes no promises about functionality, performance, or availability. Not responsible if StageFreight replaces your entire CI pipeline and you find yourself with free time you didn't expect, your retention policies work so well your registry bill drops and finance gets confused, or your release notes become more detailed than the actual features they describe.

Any resemblance to working software is entirely intentional but not guaranteed. The developer claims no credit for anything that actually goes right — that's all you and the unstoppable force of the Open Source community.

## License

Distributed under the [AGPL-3.0-only](LICENSE) License. See [licensing](docs/licensing.md) for commercial licensing.
