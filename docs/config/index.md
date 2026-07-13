# It's all `.stagefreight.yml`

StageFreight is configured through **one file at the repo root: `.stagefreight.yml`.** There
is no other config surface — no per-command flags to memorize, no scattered dotfiles. Every
capability documented here maps to keys in that one file.

This section is the **complete key reference**, grouped by concern. Each page lists the keys
it owns, the values they accept, and an **annotated YAML block** generated directly from the
code — so the options shown here can never drift from what the binary actually parses.

!!! tip "Happy path vs. every knob"
    New here? Start with **[Quick Start](../quickstart.md)** — real, running configs you
    can copy. Come to Configuration when you need a specific knob and want to know whether it
    exists yet and what it accepts.

## The buckets

Every top-level key, sorted into meaningful groups:

| Section | Top-level keys it covers |
|---|---|
| [Identity & Connectivity](identity.md) | `version` · `vars` · `defaults` · `forges` · `repos` · `registries` |
| [Builds & Tests](builds.md) | `builds` · `build_cache` · `test` |
| [Targets](targets.md) | `targets` (per-kind: registry · docker-readme · gitlab-component · release · binary-archive · generic-package · pages) |
| [Narrate](narrate.md) | `narrate` |
| [Lint](lint.md) | `lint` |
| [Policy](policy.md) | `matchers` · `ci` · `versioning` · `dependency` · `release` · `security` · `commit` · `toolchains` · `glossary` · `presentation` · `tag` · `manifest` |
| [Lifecycle Modes](lifecycle.md) | `lifecycle` · `gitops` · `governance` · `docker` |
| [Signing](signing.md) | `signing` · `signing_profiles` |

A few ideas cut across every bucket — template variables, credential resolution, retention,
and the pattern/condition syntax. They live once in **[Concepts](concepts.md)** and are
referenced from the pages above. The **[Package Distribution](package-distribution.md)** guide
covers the `generic-package` target kind in depth.

For the raw, exhaustive dump see the **[Full Schema Reference](../reference/Config.md)**
(generated). This section is the curated, explained version of the same thing.
