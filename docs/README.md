# StageFreight Documentation

## Reference

Generated reference pages — authoritative, always in sync with code:

- [CLI Reference](reference/CLI.md) — complete command, flag, and subcommand reference
- [Config Reference](reference/Config.md) — complete `.stagefreight.yml` schema reference

## Conceptual Docs

Feature-focused guides with concepts, examples, and workflows:

- [CI Integration](CI.md) — provider skeletons, required tokens and scopes, cross-pipeline advisory bridge
- [Docker Build](Docker.md) — `builds:` + `targets:` for container images, cache, retention, build strategy
- [Release Management](Release.md) — `kind: release` targets, rolling tags, cross-forge sync
- [Narrator & Badges](Narrator.md) — content composition, badge generation, `kind: include`
- [Security Scanning](Security.md) — vulnerability scanning, SBOM, detail levels
- [Linter Configuration](Linter.md) — 9 lint modules, cache TTL contract, freshness
- [Dependency Update](DependencyUpdate.md) — `dependency update` command, Go toolchain strategies
- [GitLab Components](GitLab-Components.md) — publishing GitLab CI components for your projects (and why StageFreight no longer ships itself as one)
- [Known Issues](KnownIssues.md) — active bugs and workarounds

## Examples

- [Configuration Examples](config/README.md) — 24 example `.stagefreight.yml` manifests for every project archetype
- [Quick Examples](examples/) — minimal, focused config snippets:
  - [`minimal.yml`](examples/minimal.yml) — bare minimum builds + targets
  - [`narrator.yml`](examples/narrator.yml) — badge generation + README embedding
  - [`release.yml`](examples/release.yml) — release target with rolling tags
  - [`crucible.yml`](examples/crucible.yml) — two-pass crucible build mode

## Shared Primitives

These structures are used across multiple config sections:

- **Retention Policy** — restic-style tag/release cleanup ([reference](Docker.md#retention-policy))
- **Pattern Syntax** — regex/literal/negated patterns for branches and tags ([reference](Docker.md#pattern-syntax))
- **Condition** — tag/branch-sensitive rule primitive ([reference](Security.md#condition-primitive))
- **Template Variables** — `{version}`, `{var:name}`, `{env:VAR}`, etc. ([reference](Narrator.md#template-variables))

## Planning

- [Road Map](RoadMap.md) — full product vision, phased feature plan, and [tracking index](RoadMap.md#tracking-index)
