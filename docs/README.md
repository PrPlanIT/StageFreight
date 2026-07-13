# StageFreight

A **declarative lifecycle runtime** — GitOps, Kubernetes, Docker, and CI ecosystems driven
from **one file** in your repo: [`.stagefreight.yml`](config/index.md).

StageFreight owns the build → sign → release → publish → retain lifecycle so projects don't
need ad-hoc CI scripts. It dogfoods itself: everything here (this docs site included) is built
and shipped by StageFreight.

## Start here

- **[Quick Start](quickstart.md)** — real, running `.stagefreight.yml` configs across
  archetypes; copy the one closest to yours.
- **[Configuration](config/index.md)** — the complete `.stagefreight.yml` reference, grouped
  by concern (identity, builds, targets, narrate, lint, policy, lifecycle, signing).
- **[Integrations](integrations/index.md)** — stand up a runner (build + GitOps), CI setup,
  forge/registry support.
- **[How It Works](architecture/index.md)** — the phase model and the architecture behind it.

## Reference

- [CLI Reference](reference/CLI.md) — every command, flag, and subcommand (generated).
- [Full Schema Reference](reference/Config.md) — the exhaustive `.stagefreight.yml` schema
  (generated).

## More

- [Features](features.md) — what StageFreight does vs. hand-rolled CI.
- [Screenshots](screenshots.md) — the pipeline output in practice.
- [Known Issues](known-issues.md) · [Licensing](licensing.md)
