# Props — Composable Presentation Subsystem

Props is StageFreight's typed, discoverable, validated, schema-aware presentation subsystem. Users declare presentation items in config; StageFreight resolves them through typed, validated resolvers to produce normalized structured output.

## Architecture

Props uses two-level dispatch:

- **Format** — how a prop renders. `badge` is the v1 format. Future formats (`image`, `callout`, `status-chip`) plug in without redesigning the subsystem. Format is intentionally separate from narrator's `kind` field.
- **Type** — which resolver handles the prop. Each type (e.g. `docker-pulls`, `codecov`, `slsa`) maps to a resolver that constructs URLs and metadata.

```
typed spec → registry lookup → resolver → ResolvedProp → FormatMarkdown → narrator composition
```

**Separation of concerns:**
- **Resolver** = what exists (structured data: ImageURL, LinkURL, Alt)
- **FormatMarkdown** = how it looks (single shared renderer for all resolvers)
- **Narrator placement** = where it goes (existing placement system)

### Variant

v1 implements `classic` only — standard markdown badge output. The `Variant` type exists as an implemented seam: unknown variants are rejected at validation time (not silently defaulted). Future render styles (flat, pill, branded-tile) add switch arms without redesigning the render path.

### Validation Model

Config validation (`src/config/validate.go`) checks YAML structure only: `kind: props` must have a `type` field. Semantic validation (param checking, resolution) happens at narrator run time via `ResolveDefinition()`. This is intentional deferred validation — structural errors are caught early, semantic errors surface when resolvers run with real params.

## Configuration

Props items use `kind: props` in narrator config:

```yaml
narrator:
  - file: README.md
    items:
      - id: prop.pulls
        kind: props
        type: docker-pulls
        placement:
          between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
          inline: true
        params:
          image: prplanit/stagefreight

      - id: prop.codecov
        kind: props
        type: codecov
        placement:
          between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
          inline: true
        params:
          repo: prplanit/stagefreight
          branch: main

      - id: prop.goreport
        kind: props
        type: go-report-card
        params:
          module: github.com/sofmeright/stagefreight

      - id: prop.ci
        kind: props
        type: github-actions
        params:
          repo: prplanit/stagefreight
          workflow: build.yml
          branch: main

      - id: prop.slsa
        kind: props
        type: slsa
        params:
          level: "3"
```

### Presentation Overrides

Override fields live outside `params` and apply to any type:

```yaml
      - id: prop.pulls-custom
        kind: props
        type: docker-pulls
        params:
          image: prplanit/stagefreight
        label: "Docker Pulls"     # override auto-derived alt text
        link: "https://hub.docker.com/r/prplanit/stagefreight"  # override auto-derived link
        style: flat-square        # override default badge style
        logo: docker              # override/add shields.io logo
```

| Override | Behavior |
|----------|----------|
| `label` | Overrides auto-derived alt text |
| `link` | Overrides auto-derived link URL |
| `style` | Appended as `?style=` to shields.io URLs; ignored for native/static providers |
| `logo` | Appended as `&logo=` to shields.io URLs; ignored for native/static providers |

Unsupported presentation overrides are silently ignored (not errors). They are cross-cutting hints, not semantic params. Hard errors are reserved for bad `params` values only.

### Key Rules

- `params` contains only provider-semantic inputs (`repo`, `module`, `image`, etc.)
- `style` and `logo` are **not** params — they flow through presentation overrides only
- Unknown keys in `params` produce a hard error
- Missing required params produce a hard error

## CLI Commands

### `stagefreight props list`

List all available prop types, grouped by category:

```
stagefreight props list
stagefreight props list --category docker
```

### `stagefreight props categories`

List categories with type counts:

```
stagefreight props categories
```

### `stagefreight props show <type>`

Show description, parameters, and example config for a type:

```
stagefreight props show codecov
stagefreight props show docker-pulls
```

### `stagefreight props render`

Resolve a prop and print the resulting markdown:

```
stagefreight props render --type docker-pulls --param image=prplanit/stagefreight
stagefreight props render --type go-report-card --param module=github.com/sofmeright/stagefreight
stagefreight props render --type slsa --param level=3
```

## Provider Types

### Shields (`shields`)
Types backed by img.shields.io URLs. Support `style` and `logo` presentation overrides via RenderOptions (not params).

### Native (`native`)
Types using the service's own badge URL (e.g., codecov.io, goreportcard.com, GitHub Actions). Presentation overrides for shields.io (`style`, `logo`) are ignored.

### Static (`static`)
Fixed image URLs with no dynamic provider params (e.g., SLSA level badge, conventional commits).

## Normalization Conventions

| Param | Convention | Example |
|-------|-----------|---------|
| `repo` | `owner/name` (no host prefix) | `prplanit/stagefreight` |
| `module` | Full Go module path | `github.com/sofmeright/stagefreight` |
| `image` | Docker Hub `org/name` (no registry prefix) | `prplanit/stagefreight` |
| `branch` | Defaults to empty (provider default) | `main` |

### Escaping

Template expansion (`{param}` replacement) inserts values as-is. This is safe for structured values that naturally fit URL paths (`owner/repo`, `github.com/org/name`). For arbitrary text in URL path segments or query values, resolvers apply encoding (`url.PathEscape`, `url.QueryEscape`) themselves. This keeps the common case simple while handling edge cases correctly.

## Resolution Timing

Props resolve at **build/narrator-run time**, not at view time. Resolvers construct URLs and metadata once; the output is static markdown committed to the repo. No network calls needed at resolution time.

## Type Catalog (37 Tier 1)

- **ci** (2): github-actions, circleci
- **conventions** (2): conventional-commits, semantic-release
- **docker** (4): docker-pulls, docker-stars, docker-image-size, docker-version
- **funding** (2): github-sponsors, paypal-donate
- **github** (6): github-issues-open, github-issues-closed, github-prs-closed, github-last-commit, github-commit-activity, go-version
- **misc** (1): visitor-count
- **quality** (3): codecov, go-report-card, go-reference
- **release** (5): github-release, github-tag, github-license, github-contributors, artifact-hub
- **security** (5): openssf-scorecard, openssf-best-practices, fossa-license, fossa-security, slsa
- **social** (7): slack, discord, twitter, bluesky, linkedin, website, contact

**Total: 37 types across 10 categories.**

Use `stagefreight props list` for the full list with descriptions and provider info.
