# Quick Start Scenarios

The fastest way to learn StageFreight is to read a **real `.stagefreight.yml` that's actually
running.** Below are live configs across different project archetypes — open the one closest
to yours, copy the shape, then reach for [Configuration](../configuration/index.md) when you
want a knob it doesn't show.

!!! tip "This is the happy path"
    These configs are deliberately representative, not exhaustive. If you want a capability
    you don't see here, check [Configuration](../configuration/index.md) to see whether the
    knob exists yet and what it accepts.

## Pick the scenario closest to yours

| Scenario | Archetype | Knobs it demonstrates | Live config |
|---|---|---|---|
| **Container app (full lifecycle)** | Dockerfile image, dev + stable channels | `builds`, `kind: registry`, `kind: docker-readme`, `narrate` badges, retention | [DD-UI](https://github.com/PrPlanIT/DD-UI/blob/main/.stagefreight.yml) |
| **CLI / binary distribution** | Go binary + image + downloadable archives | `kind: binary`, `kind: binary-archive`, `kind: release` with checksums | [HASteward](https://github.com/PrPlanIT/HASteward/blob/main/.stagefreight.yml) · [Dragonfly](https://github.com/HomeLabHD/dragonfly/blob/main/.stagefreight.yml) · [Jetpack](https://github.com/HomeLabHD/jetpack/blob/main/.stagefreight.yml) |
| **GitOps repo** | Flux manifest validation, no image build | `lifecycle: { mode: gitops }`, cluster auth | [Dungeon](https://github.com/SoFMeRight/Dungeon/blob/main/.stagefreight.yml) |
| **Governance / control repo** | Policy reconciliation across repos | `lifecycle: { mode: governance }` | [MaintenancePolicy](https://github.com/PrPlanIT/MaintenancePolicy/blob/main/.stagefreight.yml) |
| **Dogfood: everything at once** | StageFreight building itself | every target kind, `kind: command` docs build, self-hosted release channels | [StageFreight](https://github.com/PrPlanIT/StageFreight/blob/main/.stagefreight.yml) |

!!! note "More scenarios coming"
    A couple of archetypes aren't public yet and will be added as those repos open up:
    **static site → Cloudflare Pages** (`kind: pages`) and **Ansible collection**
    (`kind: gitlab-component`). Until then, [Configuration › Targets](../configuration/targets.md)
    documents both.
