# Stencils & Scribe

**Stencils** are StageFreight's reusable text elements — badges, shields, provider badges,
included fragments, inventory tables — each defined once in the top-level `stencils:` library
and embeddable anywhere as `{id}`. **Scribe** is the phase that places rendered stencils into
repo files (README badge rows, marked regions) and commits them back.

The `stencils:` library is a shared composition surface: the same `{id}` elements feed scribe's
file regions, and (as they land) narrate summaries and release bodies — one grammar, many
destinations. Configure `stencils:` + `scribe:` and scribe runs (presence-enabled).

## `stencils:` — define once

Every renderable is declared once under the top-level `stencils:` key, by id. Embed it as
`{id}` in a scribe region (below) and it renders to markdown. Three flavors:

**Inline badge** (default) — a local SVG you own, drawn from your data:

```yaml
stencils:
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

!!! note "Reserved ids"
    A stencil id must not shadow a gitver template keyword (`base`, `sha`, `version`, `branch`,
    `major`/`minor`/`patch`, `date`, …). `{base}` always resolves to the version fact, so a
    stencil named `base` would be unreachable — validation rejects it.

## `scribe.files` — place by `{id}`

`scribe.files` is an **id → region map**; each region names a file and its markers. Content
between the markers is replaced idempotently each run; everything outside them is never touched.
Fill a region one of two ways:

**`body:`** — freeform markdown with `{id}` stencil embeds (and `{#if}` conditionals). Write the
region exactly as you want it to read:

```yaml
scribe:
  files:
    readme.hero:
      file: README.md
      between: ["<!-- sf:hero:start -->", "<!-- sf:hero:end -->"]
      body: |
        {build} {license} {release}

        **{project.name}** — {project.description}
```

**`items:`** — sugar for a plain row of stencils (`br` for a row break, `inline:` for
side-by-side). Equivalent to a `body:` of space-joined `{id}` embeds:

```yaml
    readme.badges:
      file: README.md
      between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
      inline: true          # space-joined (true) | rows split on "br" (false)
      items: [build, license, release, br, docker]   # stencil ids (+ "br" for a break)
```

The stencil lives once under `stencils:` and is referenced by `{id}` — no `badge_ref`, no
per-file re-declaration. Any `{…}` the stencil engine doesn't recognize falls through to the
gitver template pass (`{base}`, `{env:X}`, `{project.*}`, `{registry.<id>.*}`, …), so version/env facts
work directly in a `body:` too. A `link_base` on the file fixes relative links and derives the
raw-content base for badge image sources.

## `scribe.commit` — land it

```yaml
scribe:
  commit:
    type: docs
    message: "refresh generated docs and badges"
    add: [".stagefreight/badges", "README.md"]
    push: true
```

## `type: llm` — AI stencils

An llm stencil composes an input and sends it through an `llms:` backend
([Identity & Connectivity](identity.md)); the model's response renders as the stencil's
output. The `body:` is the composed INPUT — facts and `{stencil}` embeds resolve first, so
the model receives the real postmortem or changelog, and the ask is written inline:

```yaml
stencils:
  triage:
    type: llm
    llm: local          # llms: entry id
    body: |
      You are a CI triage assistant. In two sentences, explain the most likely
      cause of this failure and the first thing to check.

      {postmortem}
```

Contracts: **degrade to empty** (an unreachable backend renders nothing — the pipeline never
fails because a model was down), **per-run memoization** (one generation per stencil per run),
and `<think>…</think>` reasoning traces are stripped. AI output is **dispatch-only**: llm
stencils — and any text stencil transitively embedding one — are rejected by validation in
scribe file regions; the committed record stays deterministic. The deliberate exception is
release bodies, where embedding AI is the author's editorial choice. See
[Narration & Notifications](narration.md) for dispatch examples.

## CLI

| Command | What it does |
|---|---|
| `stagefreight scribe apply` | Render every region and reconcile it into the marker spans (the phase, run locally). |
| `stagefreight scribe render <id>` | Render one stencil's markdown to stdout. |
| `stagefreight scribe types` | Browse stencil producer types, or detail one. |

## Reference

--8<-- "docs/assets/modules/config-reference.md:stencils"

--8<-- "docs/assets/modules/config-reference.md:scribe"
