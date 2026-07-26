# Scribe

The Scribe phase generates repo-facing content — README badges, shields, provider badges,
and included fragments — and commits it back. Presence-enabled: configure `scribe:` and it
runs.

`scribe:` has three parts:

| Key | What it does |
|---|---|
| `content` | Define each renderable **once**, keyed by id — a source rendered through a render. Inline defs render a badge (default) or a shield (`render: shield`); a `type:` names a producer module. |
| `files` | Marker regions in a file; each region's `items` are **content ids by name** (plus `br` for a line break) — no re-declaration. |
| `commit` | The commit that lands the generated content and patched files back into the repo. |

## `content` — define once

Every renderable is declared once under `content`, keyed by its id. Three flavors:

**Inline badge** (default) — a local SVG you own, drawn from your data:

```yaml
scribe:
  content:
    build:
      label: build
      message: "{env:BUILD_STATUS}"
      color: auto                       # or a hex like "#74ecbe"
      font: dejavu-sans
      output: ".stagefreight/badges/build.svg"
      link: "https://…/pipelines"
```

**Inline shield** (`render: shield`) — a shields.io badge composed from the same fields (no `%2F` juggling):

```yaml
    docker: { render: shield, label: Docker, message: "{var:org}/{var:repo}", color: "#2496ED", logo: docker, link: "https://hub.docker.com/r/{var:org}/{var:repo}" }
```

**Named producer** (`type:`) — an ecosystem/data producer resolved by a typed module (repo/module inferred from your `repos:` publish-origin; the common ones need no params):

```yaml
    go-report:     { type: goreportcard }
    contributors:  { type: github-contributors }
    contents-base: { type: contents, build: myapp, section: inventories.versions, render: badges }
```

Browse every producer type with `stagefreight scribe types`.

## `files` — place by name

`files` is an **id → region map**; each region names a file, its markers, and an ordered list
of content **ids**. Content between the markers is replaced idempotently each run; everything
outside them is never touched.

```yaml
scribe:
  files:
    readme-badges:                                    # region id
      file: README.md
      between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
      mode: replace          # replace (default) | append | prepend | above | below
      inline: true           # space-joined (true) | newline-joined (false)
      items: [build, license, release, br, docker]   # content ids (+ "br" for a break)
```

The def lives once under `content` and is referenced by name here — there is no `badge_ref`
and no per-file re-declaration. `br` forces a line break between composed items. A `link_base`
on the file fixes relative links and derives the raw-content base for badge image sources.

## `commit` — land it

```yaml
scribe:
  commit:
    type: docs
    message: "refresh generated docs and badges"
    add: [".stagefreight/badges", "README.md"]
    push: true
```

## CLI

| Command | What it does |
|---|---|
| `stagefreight scribe apply` | Render all content and reconcile it into the marker regions (the phase, run locally). |
| `stagefreight scribe render <id>` | Render one declared content item's markdown to stdout. |
| `stagefreight scribe types` | Browse content producer types, or detail one. |

## Reference

--8<-- "docs/assets/modules/config-reference.md:scribe"
