# Quick Start Scenarios

The fastest way to learn StageFreight is to read a **real `.stagefreight.yml` that's actually
running.** Below are live configs across different project archetypes — open the one closest
to yours, copy the shape, then reach for [Configuration](config/index.md) when you
want a knob it doesn't show.

!!! tip "This is the happy path"
    These configs are deliberately representative, not exhaustive. If you want a capability
    you don't see here, check [Configuration](config/index.md) to see whether the
    knob exists yet and what it accepts.

## Pick the scenario closest to yours

| Scenario | Archetype | Knobs it demonstrates | Live config |
|---|---|---|---|
| **Container app (full lifecycle)** | Dockerfile image, dev + stable channels | `builds`, `kind: registry`, `kind: docker-readme`, `narrate` badges, retention | [DD-UI](https://github.com/PrPlanIT/DD-UI/blob/main/.stagefreight.yml) |
| **CLI / binary distribution** | Go binary + image + downloadable archives | `kind: binary`, `kind: binary-archive`, `kind: release` with checksums | [HASteward](https://github.com/PrPlanIT/HASteward/blob/main/.stagefreight.yml) · [Dragonfly](https://github.com/HomeLabHD/dragonfly/blob/main/.stagefreight.yml) · [Jetpack](https://github.com/HomeLabHD/jetpack/blob/main/.stagefreight.yml) |
| **GitOps repo** | Flux manifest validation, no image build | `lifecycle: { mode: gitops }`, cluster auth | [Dungeon](https://github.com/SoFMeRight/Dungeon/blob/main/.stagefreight.yml) |
| **Governance / control repo** | Policy reconciliation across repos | `lifecycle: { mode: governance }` | [MaintenancePolicy](https://github.com/PrPlanIT/MaintenancePolicy/blob/main/.stagefreight.yml) |
| **Static site → Cloudflare Pages** | Docs site built + deployed on release | `kind: command` (mkdocs build), `kind: pages` (Cloudflare) | [StageFreight](https://github.com/PrPlanIT/StageFreight/blob/main/.stagefreight.yml) |
| **Dogfood: everything at once** | StageFreight building itself | every target kind, `kind: command` docs build, `kind: pages`, self-hosted release channels | [StageFreight](https://github.com/PrPlanIT/StageFreight/blob/main/.stagefreight.yml) |

## From config to a running pipeline

A `.stagefreight.yml` describes *what* to do. Two more steps turn it into a live pipeline:

1. **Render your CI file.** StageFreight owns the pipeline document — you don't hand-write or
   copy it, you render it from your config and commit the result:

    ```bash
    stagefreight ci render github --write     # or: gitlab · gitea · forgejo
    git add .github/workflows/stagefreight.yml
    stagefreight commit -t ci -m "render pipeline"
    ```

    Each forge has its own output path and token — see [CI Setup](integrations/ci.md).

2. **Have a runner.** **GitHub Actions runs on GitHub-hosted runners natively** — nothing to
   stand up. GitLab and other self-hosted setups need a runner with Docker + BuildKit;
   [Integrations](integrations/index.md) carries the runner deployments.

Then **push** (or **tag**) and the pipeline runs **audition → perform → review → publish →
narrate**. Forge tokens and registry credentials resolve from CI variables at run time — see
[Concepts → Credentials](config/concepts.md#credential-resolution).

!!! note "More scenarios coming"
    A **GitLab CI/CD component** publisher (`kind: gitlab-component` — pushes reusable
    pipeline components to the GitLab component catalog) will be added once a public example
    repo opens up. Until then, [Configuration › Targets](config/targets.md)
    documents it.
