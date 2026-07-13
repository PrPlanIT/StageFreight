# Narrate

The Narrate phase composes repo-facing content — README badges, shields, and included
fragments — and commits it back. Presence-enabled: configure `narrate:` and it runs.

`narrate:` has three parts:

| Key | What it does |
|---|---|
| `badges` | Defines **local SVG badges** you own (label, value, color, font, output path, link). Rendered by `stagefreight badge generate`. |
| `patches` | For each `file:`, a list of `items:` composed into marker regions (`kind: badge_ref · shield · props · text · include · component · break`). |
| `commit` | The commit that lands the generated badges and patched files back into the repo. |

## Two badge systems

StageFreight has **two** ways to put a badge in your README, and they're easy to confuse.
Both are narrate item kinds and can sit on the same line:

| | `badges` + `kind: badge_ref` | `kind: props` |
|---|---|---|
| **What** | A local SVG generator you **own** | A registry of **external** provider badges |
| **Powered by** | the `badge` package (renders SVG) | the `props` package (resolver/composer) |
| **Output** | committed `.svg` files — branded, version-stamped | markdown → an external URL with live data |
| **Sources** | your own data | shields.io, codecov, Go Report Card, docker-pulls, SLSA… |
| **CLI** | `stagefreight badge generate` | `stagefreight props list` · `props render` |
| **Use when** | you control the data and want branded assets | you want live ecosystem data in a standard format |

In short: **local badges = "I'll draw my own." props = "give me the shields.io one for
docker pulls."** Both compose through narrate, so they coexist happily in one README.

## How it works

Define a badge once under `badges`, then reference it from a file's `patches` with
`kind: badge_ref` (or inline a one-off with `kind: badge`):

```yaml
narrate:
  badges:
    - id: release
      text: release             # left label
      value: "v{base}"          # right side (template-expanded)
      color: "#74ecbe"          # hex, or "auto" (status-driven)
      font: dejavu-sans
      output: ".stagefreight/badges/release.svg"
      link: "https://github.com/myorg/myrepo/releases"

  patches:
    - file: "README.md"
      link_base: "https://github.com/myorg/myrepo/blob/main"
      items:
        - id: badge.release
          kind: badge_ref
          ref: release          # → the badges[] id above
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
            mode: replace
            inline: true

        - id: shield.pulls
          kind: shield
          shield: "docker/pulls/myorg/myrepo"
          link: "https://hub.docker.com/r/myorg/myrepo"
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
            mode: replace
            inline: true

  commit:
    type: docs
    message: "refresh generated badges"
    add: [".stagefreight/badges", "README.md"]
```

Items sharing the same placement markers are composed together — inline items are
space-joined, block items newline-joined.

### Item kinds

| Kind | Purpose |
|------|---------|
| `badge` | Inline SVG badge defined in place (label + value + color + `output`). |
| `badge_ref` | References a badge defined under `narrate.badges` by its `ref`. |
| `shield` | Shields.io shorthand — the path is appended to `https://img.shields.io/`. |
| `props` | An external provider badge from the props registry (see below). |
| `text` | Literal markdown with [template variables](concepts.md#template-variables). |
| `include` | Verbatim file inclusion — reads a file and inserts it as-is (used to assemble generated reference fragments into wrapper pages). |
| `component` | Input documentation rendered from a GitLab CI component spec. |
| `break` | Forces a line break between composed items. |

### Placement

`placement` declares where an item's output goes, between two markers. Content between the
markers is replaced idempotently on each run; everything outside them is never touched.

```yaml
placement:
  between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
  mode: replace     # replace (default) | append | prepend | above | below
  inline: true      # space-joined (true) or newline-joined (false)
```

### URL resolution

A file's `link_base` fixes relative links, and StageFreight derives a `raw_base` from it for
badge image sources:

| Forge | `link_base` | Derived `raw_base` |
|-------|-------------|---------------------|
| GitHub | `github.com/{owner}/{repo}/blob/{branch}` | `raw.githubusercontent.com/{owner}/{repo}/{branch}` |
| GitLab | `gitlab.com/{owner}/{repo}/-/blob/{branch}` | `gitlab.com/{owner}/{repo}/-/raw/{branch}` |
| Gitea | `{host}/{owner}/{repo}/src/branch/{branch}` | `{host}/{owner}/{repo}/raw/branch/{branch}` |

Absolute `link` values are used as-is; relative ones resolve against `link_base`.

## Props — external provider badges

`kind: props` pulls an ecosystem-standard badge (docker-pulls, codecov, SLSA, …) from a
typed, validated resolver. `params` carries the provider-semantic inputs; presentation
overrides (`label`, `link`, `style`, `logo`) sit outside `params`.

```yaml
      - id: prop.pulls
        kind: props
        type: docker-pulls
        params:
          image: prplanit/stagefreight
        placement:
          between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
          inline: true
```

Props resolve at narrate-run time into static markdown committed to the repo — no view-time
network calls. Unknown `type`s and unknown/missing `params` are hard errors; unsupported
presentation overrides are ignored.

!!! tip "Discovering props"
    `stagefreight props categories`, `stagefreight props list`, and `stagefreight props show
    <type>` enumerate the available prop types (docker-pulls, codecov, slsa, github-actions,
    go-report-card, …) with their params and an example.

## Reference

--8<-- "docs/assets/modules/config-reference.md:narrate"
