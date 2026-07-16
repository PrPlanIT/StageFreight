# `.stagefreight.yml` shape proposals — scratchpad

Working doc to iterate the config surface (starting with `narrate`) toward something
**tidier and compose-ergonomic without losing any capability**. Nothing here is
committed to the schema yet — it's a design scratchpad. The live `.stagefreight.yml` is
the reference for "what must still be expressible."

## Goal / constraints

1. **Lossless.** Everything the current schema can do must survive: explicit markers,
   `mode` (replace/append/prepend), `inline`, item **order**, multiple regions per file,
   multiple files, per-item overrides, and all item kinds (`badge_ref`, `shield`, `props`,
   `include`, `build-contents`).
2. **Compose-ergonomic.** Reads top-to-bottom, minimal repetition, no boilerplate you
   copy-paste 25 times. Should feel like something you *want* to write.
3. **Minimal relearning.** Keep the vocabulary that already means something (`kind`,
   `ref`, `between`) unless collapsing it is a clear win.

---

## Baseline (today) — the fatigue

Every item re-states the same placement. ~25× this, across 3 regions in one file:

```yaml
narrate:
  patches:
    - file: README.md
      items:
        - id: badge.build
          kind: badge_ref
          ref: build
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
            mode: replace
            inline: true
        - id: badge.license
          kind: badge_ref
          ref: license
          placement:
            between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]   # ← identical, again
            mode: replace
            inline: true
        # …23 more, the placement copied verbatim each time
```

Full `narrate:` today ≈ **322 lines** (badges defs + patches + commit).

---

## Proposal A — `files:` + `items:` maps, placement hoisted  ← current lean

Placement is a property of the **region**, declared once; items inherit it. Items are a
**map keyed by id** (drops the `id:` field). One `files:` entry per marker region; the
key is just a label.

```yaml
narrate:
  files:
    readme.badges:                                   # label; groups one marker region
      file: README.md
      between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
      mode: replace
      inline: true                                   # region defaults → inherited by items
      items:
        - build:   { kind: badge_ref, ref: build }
        - license: { kind: badge_ref, ref: license }
        - donate:  { kind: shield, shield: "badge/donate-FF5E5B?logo=ko-fi", link: "https://ko-fi.com/…" }

    readme.project:
      file: README.md
      between: ["<!-- sf:project:start -->", "<!-- sf:project:end -->"]
      inline: true                                   # mode:replace is the default
      items:
        - go-report:    { kind: props, type: go-report-card }
        - issues-open:  { kind: props, type: github-issues-open }
        - contributors: { kind: props, type: github-contributors }

    readme.image:
      file: README.md
      between: ["<!-- sf:image:start -->", "<!-- sf:image:end -->"]
      inline: true
      items:
        - docker: { kind: shield, shield: "…" }
        - br                                          # explicit row break, keeps ordering
        - latest: { kind: shield, shield: "…" }
        - size:   { kind: shield, shield: "…", mode: append }   # per-item override still available
```

### What this keeps (the lossless check)
- **Explicit markers** — `between:` is stated, per region (not magic'd from a name).
- **`mode` / `inline`** — region default, **overridable per item** (`size` above).
- **Order + breaks** — item order = row order; `kind: break` is an explicit break.
- **Multi-region / multi-file** — one `files:` entry per region; `file:` can be anything.
- **All kinds** — `kind:` is retained verbatim.

### Redundancies it removes
- Placement stated **once per region** instead of per item (the big win).
- `id:` gone (item key = id).

### Further squeezes (optional, decide below)
- `output` for render badges → `.stagefreight/badges/{id}.svg` (always).
- `ref` for `badge_ref` → **defaults to the item key**, so `build: { kind: badge_ref }`.
- `text` for a render badge → defaults to its key.

---

## Proposal B — badge render defs (resolves Q5)

Defs are a **map keyed by id** — safe here because a def registry is order-free (badges
are pulled by `ref`; row order lives in the placement list). The key *is* the id and the
default `text`. Shared render settings hoist to `defaults:`; per-badge fields override.

Baseline — 7 lines/badge, `id`+`text`+`output`+`font` all restated ~13×:

```yaml
badges:
  - id: build
    text: build
    value: "{env:BUILD_STATUS}"
    color: auto
    font: monofur
    output: ".stagefreight/badges/build.svg"
    link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}/-/pipelines"
```

Proposal B — key=id=text, `output` derived, render settings default:

```yaml
badges:
  defaults: { font: monofur, color: auto }           # shared; per-badge fields override
  build:
    value: "{env:BUILD_STATUS}"
    link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}/-/pipelines"
  license:
    value: "AGPL-3.0-only"
    color: "#310937"
    link: LICENSE
  release:
    value: "v{base}"
    color: "#74ecbe"
    font: dejavu-sans                                 # overrides the default
    font_size: 11
    link: "https://github.com/{var:github_org}/{var:github_repo}/releases"
  release-latest:
    text: latest                                      # text ≠ id → stated explicitly
    value: "v{base}"
    color: "#74ecbe"
    link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest"
```

### Collapse rules
- **key → id**, and the id is the default `text` (state `text:` only when it differs).
- **`output` gone** — always `.stagefreight/badges/{id}.svg` (override only for the rare exception).
- **`font` / `font_size` / `color` → `defaults:`**, overridable per badge.
- **`value` / `link`** stay per badge — that's the actual content, not boilerplate.

Net: 7 lines → 2–3 per badge; the shared `font`/`color` stated once.

---

## Proposal C — one item grammar for every kind (resolves Q3 + Q4)

Every kind already carries a field that **uniquely names it** — so `kind:` is redundant,
infer it from the payload field. With placement hoisted to the region (Prop A) and id=key,
each item collapses to one line.

| payload field       | kind (inferred)      | output                          |
|---------------------|----------------------|---------------------------------|
| bare key, or `ref:` | `badge_ref`          | local SVG (`src/badge`)         |
| `shield:`           | `shield`             | raw external image (shields.io) |
| `prop:` (was `type:`)| `props`             | registry external (`src/props`) |
| `include:`          | `include`            | injected docs fragment          |
| `contents:` (was `build:`)| `build-contents`| assembled from build manifest   |
| `br` / `break`      | `break`              | row break                       |

`prop:` and `contents:` are renames so the field *is* the discriminator, parallel to
`shield:`/`include:`. `type:`/`build:` still accepted.

Props (9 lines today) → one line; the raw shield beside it, same shape:

```yaml
items:
  - go-report: { prop: go-report-card, params: { module: "github.com/{var:github_org}/{var:github_repo}" } }
  - prs-open:  { shield: "github/issues-pr/{var:github_org}/{var:github_repo}" }
  - donate:    { shield: "badge/donate-FF5E5B?logo=ko-fi", link: "https://ko-fi.com/…" }
```

**Q3 (props vs shield).** They ARE the same category — an external image; a prop is a
*registry-named* shield (`src/props` builds the URL from `params`) vs a raw shields.io
path. But don't add a `source:` discriminator — that's *more* config. Field presence
(`prop:` vs `shield:`) already tells them apart, and reads as what it is.

**Q4 (include / build-contents).** They're **single-item regions** — each owns its own
markers and injects a *block* (not an inline image). Same `files:` shape, one item:

```yaml
files:
  cli-reference:
    file: docs/reference/CLI.md
    between: ["<!-- sf:cli-reference:start -->", "<!-- sf:cli-reference:end -->"]
    items:
      - { include: docs/assets/modules/cli-reference.md }

  contents.base:
    file: README.md
    between: ["<!-- sf:contents-base:start -->", "<!-- sf:contents-base:end -->"]
    items:
      - { contents: stagefreight, section: inventories.versions, renderer: badges }
```

---

## Open questions to resolve here

1. **Item ordering vs map.** A YAML map is unordered in most Go parsers, but **badge row
   order matters.** Options: (a) parse `items:` as an *ordered* map (insertion order via
   `yaml.Node`), or (b) make `items:` a list where each entry carries a `ref:`/key. Lean?
   *(Map reads nicer; needs ordered-map parsing to be safe.)*

2. **Multiple regions per file.** Proposal A uses one keyed `files:` entry per region
   (`readme.badges`, `readme.project`, …), so `file: README.md` repeats. Alternatives:
   a `regions:` sub-list under a single `readme:` file entry. Which reads better?

3. **`kind` collapse (props vs badges vs shield).** **Resolved — see Proposal C.** Drop
   `kind:`; infer from the payload field (`ref`/`shield`/`prop`/`include`/`contents`/`br`).
   Reject the `source:` discriminator — it adds config; field presence already discriminates.

4. **Non-badge items.** **Resolved — see Proposal C.** `include` / `build-contents` are
   single-item regions (own markers, inject a block). Same `files:`/`items:` shape, one item.

5. **Where do render definitions live?** **Resolved — see Proposal B.** Top-level `badges:`
   map (keyed by id) holds render defs; `files:`/`items:` do placement, referencing defs by
   key. Safe as a map because defs are order-free (order lives in the placement list).

---

## Scratchpad — iterations

<!-- paste/edit shapes below; we converge here before touching the schema -->

Full config in the target shape — keyed-by-id throughout, `narrate` → props/files/commit. The
eyeball target for schema v1.0. (Order-free registries `forges`/`repos`/`registries` are plain
maps; keyed-collections `versioning.*`/`builds`/`targets` assume the insertion-order-preserving
parse — Q1 — since their order is load-bearing.)

```yaml
version: 1
image: docker.cr.pcfae.com/prplanit/stagefreight:latest-dev     # CI runtime image (root; per-build image: overrides)
# description + license are auto-detected — license ← LICENSE file (`{project.license}`), description ← publish-origin repo. Declare at root only to override.

vars:
  org: prplanit
  github_org: PrPlanIT
  gitlab_group: PrPlanIT
  repo: stagefreight
  github_repo: StageFreight

git:                                     # interpret the ref → named patterns + the versions they imply (was matchers + versioning)
  branches:
    main: "^main$"                        # was top-level matchers.branches
  tags:                                   # was versioning.tag_sources — named tag patterns
    stable:     { pattern: "^v?\\d+\\.\\d+\\.\\d+$" }
    prerelease: { pattern: "^v?\\d+\\.\\d+\\.\\d+-.+" }
  versioning:                             # derivation rules that consume the patterns above
    branch_builds:
      default: { base_from: [stable], format: "{base}-dev+{sha}" }   # `format` produces {version} off-tag — rename to version: if you like
    no_lineage: { mode: error }

builds:
  stagefreight:
    kind: docker
    build_mode: crucible
    platforms: [linux/amd64]
  stagefreight-bin:
    kind: binary
    builder: go
    from: ./src/cli
    output: stagefreight
    env: { CGO_ENABLED: "0" }
    args:
      - "-ldflags"
      - "-s -w -X github.com/PrPlanIT/StageFreight/src/version.Version={version} -X github.com/PrPlanIT/StageFreight/src/version.Commit={sha} -X github.com/PrPlanIT/StageFreight/src/version.BuildDate={date}"
    platforms: [linux/amd64, linux/arm64]
  reference:                                         # generate docs from THIS commit's binary
    kind: command
    stage: { from: stagefreight-bin, as: stagefreight }
    command: [./stagefreight, docs, generate, --output-dir, "{output}"]
    outputs:
      - { type: tree, source: docs/assets/modules, worktree: true }
  docs-site:
    kind: command
    image: docker.io/library/python:3.12-slim
    command: "pip install --quiet --root-user-action=ignore mkdocs-material && mkdocs build --strict --site-dir {output}"
    outputs:
      - { type: tree, source: site }

# Can have variants of a forge for diff creds. It serves as unique forge accounts? Perhaps inferring github would be nice. Not having to declare. Same with registries.
forges:
  gitlab: { provider: gitlab, url: "https://gitlab.prplanit.com", credentials: GITLAB }
  github: { provider: github, url: "https://github.com",          credentials: GITHUB }

repos:
  primary:
    forge: gitlab
    project: "{var:gitlab_group}/{var:repo}"
    roles: [primary]
    branches: { default: main }
    worktree: "."
  github-mirror:
    forge: github
    project: "{var:github_org}/{var:github_repo}"
    roles: [mirror, publish-origin]

registries:
  dockerhub: { provider: docker, url: docker.io,   credentials: DOCKER, default_path: "{var:org}/{var:repo}" }
  harbor:    { provider: harbor, url: cr.pcfae.com, credentials: HARBOR, default_path: "{var:org}/{var:repo}" }
  ghcr:      { provider: ghcr,   url: ghcr.io,      credentials: GHCR,   default_path: "{var:org}/{var:repo}" }

sync:                                    # replication — what each identity keeps mirrored (scannable, keyed by id)
  github-mirror: { git: true, releases: true }   # repo → content mirror from primary

publish:                                 # was `targets:` — distribute artifacts to their destination
  # ── registry channels: one target per channel, fanned across registries (registry: takes a list) ──
  stable:     { kind: registry, registry: [dockerhub, ghcr, harbor], build: stagefreight, tags: ["v{version}", "latest"], when: { git_tags: [stable], events: [tag] } }
  prerelease: { kind: registry, registry: [dockerhub, ghcr],         build: stagefreight, tags: ["v{version}"],           when: { git_tags: [prerelease], events: [tag] } }
  dev:
    kind: registry
    registry: [dockerhub, ghcr, harbor]              # harbor now inherits retention too — want it different? separate entry
    build: stagefreight
    tags: ["dev-{sha:8}", "latest-dev"]
    when: { branches: [main], events: [push] }
    retention: { keep_last: 6, protect: ["latest-dev"] }
  harbor-test:                                        # stays: Harbor-only, !main, own tag namespace
    kind: registry
    registry: harbor
    build: stagefreight
    tags: ["test-{branch}-{sha:8}", "latest-test-{branch}"]
    when: { branches: ["!main"], events: [push] }
    retention: { keep_last: 6, protect: ["latest-test-{branch}"] }
  registry-meta:                                     # push project metadata to registries that support it; description defaults from publish-origin
    kind: metadata
    registry: [dockerhub, harbor]                    # fans; ghcr omitted (no description API)
    description: true                                # SHORT — from publish-origin; engine truncates + WARNS per provider cap (Docker Hub ~100 is tightest). Hand-fit with a string, or split targets for a genuinely different one.
    readme: README.md                                # LONG / full_description, where the provider has one · logo: only where project-scoped
    when: { branches: [main], events: [push, tag] }
  stagefreight-binaries:                   # ONE archive recipe — {version} differentiates stable (1.2.3) from dev (1.2.3-dev+sha)
    kind: binary-archive
    build: stagefreight-bin
    name: "stagefreight-{version}-{os}-{arch}"
    format: auto
    checksums: true
    when:                                  # fires on EITHER trigger — needs when: to accept a list of condition-sets (OR)
      - { git_tags: [stable], events: [tag] }   # feeds primary-release
      - { branches: [main], events: [push] }    # feeds dev-release
  primary-release:                         # authored on primary; the github mirror receives it via repos.github-mirror.sync.releases (+ its binaries) — sync is a repo verb, no per-mirror target, no sync_release flag
    kind: release
    type: latest
    archives: stagefreight-binaries
    aliases: ["v{version}", "latest"]
    when: { git_tags: [stable], events: [tag] }
  dev-release:
    kind: release
    type: prerelease
    archives: stagefreight-binaries               # the same one recipe as primary-release
    tag: "dev-{sha:8}"
    aliases: ["latest-dev"]
    retention: { keep_last: 6, protect: ["latest-dev"] }
    when: { branches: [main], events: [push] }
  docs:
    kind: pages
    provider: cloudflare
    project: stagefreight
    build: docs-site
    domain: stagefreight.prplanit.com
    when: { git_tags: [stable], events: [tag] }
  docs-github:
    kind: pages
    provider: github
    project_id: "{var:github_org}/{var:github_repo}"
    credentials: GITHUB
    build: docs-site
    when: { git_tags: [stable], events: [tag] }

narrate:
  # ── props: ONE uniform shape → { type: <what>, …fields }. `type:` is the single discriminator ──
  #    — it names the producer/renderer; the remaining fields are that producer's inputs. No provider:/topic:.
  #    RENDERERS you compose (supply the verbs):  badge = SF renders locally · shields = shields.io renders
  #    NAMED producers (self-contained; repo/module resolve from repos:):  goreportcard · go · github-issues-open · github-* · star-history (block)
  #    STRUCTURAL:  contents = build-manifest section · include = docs fragment
  #  BADGE areas, one verb each: logo/label/message (icon/left/right) · logoColor/labelColor/color · link · font/font_size (local only)
  #  label: defaults to the prop's KEY when omitted (build → "build"). State it to differ (github → "GitHub"), to
  #    reuse a display label across uniquely-keyed props (release-updated & dev-updated both "updated"), or when the
  #    text would break YAML / force a duplicate key. Both forms are accepted; examples below always encode explicit.
  props:
    # ── badges · rendered locally (SF resolves {vars}, draws the SVG) ──────────────────────────
    build:            { type: badge,    label: build,      message: "{env:BUILD_STATUS}",           color: auto,      font: monofur,                 link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}/-/pipelines" }
    license:          { type: badge,    label: license,    message: "{project.license}",            color: "#310937", font: monofur,                 link: LICENSE }
    release:          { type: badge,    label: release,    message: "v{base}",                      color: "#74ecbe", font: dejavu-sans, font_size: 11, link: "https://github.com/{var:github_org}/{var:github_repo}/releases" }
    updated:          { type: badge,    label: updated,    message: "{env:BUILD_DATE}",             color: "#236144", font: dejavu-sans, font_size: 11 }
    pulls:            { type: badge,    label: pulls,      message: "{docker.pulls}",               color: "#1d63ed",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}" }
    release-latest:   { type: badge,    label: latest,     message: "v{base}",                      color: "#74ecbe",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest" }
    release-updated:  { type: badge,    label: updated,    message: "{docker.tag.v{base}.updated}", color: "#236144" }
    release-size:     { type: badge,    label: size,       message: "{docker.tag.v{base}.size}",    color: "#555",                                   link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=v{base}" }
    dev-latest:       { type: badge,    label: latest-dev, message: "dev-{sha:8}",                  color: "#3b82f6",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest-dev" }
    dev-updated:      { type: badge,    label: updated,    message: "{docker.tag.latest-dev.updated}", color: "#236144" }
    dev-size:         { type: badge,    label: size,       message: "{docker.tag.latest-dev.size}", color: "#555",                                   link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest-dev" }
    # ── badges · type: shields → rendered by shields.io from the same verbs (no %2F / path juggling) ──
    donate:           { type: shields,  message: donate,  color: "#FF5E5B", logo: ko-fi,          logoColor: white, link: "https://ko-fi.com/T6T41IT163" }
    sponsor:          { type: shields,  message: sponsor, color: "#EA4AAA", logo: githubsponsors, logoColor: white, link: "https://github.com/sponsors/{var:github_org}" }
    github:           { type: shields,  label: GitHub, message: source, color: "#181717", logo: github,                  link: "https://github.com/{var:github_org}/{var:github_repo}" }
    gitlab:           { type: shields,  label: GitLab, message: source, color: "#FC6D26", logo: gitlab,                  link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}" }
    docker:           { type: shields,  label: Docker, message: "{var:org}/{var:repo}", color: "#2496ED", logo: docker, logoColor: white, link: "https://hub.docker.com/r/{var:org}/{var:repo}" }
    # ── badges · named producers — self-contained; repo/module resolve from repos: (override: repo: <id>) ──
    go-report:        { type: goreportcard }
    go-reference:     { type: go.dev,  topic:  go-reference  }
    last-commit:      { type: shields,  topic: github-last-commit }
    issues-open:      { type: shields,  topic: github-issues-open }
    prs-open:         { type: shields,  topic: github-prs-open }
    contributors:     { type: shields,  topic: github-contributors }
  # block widget — a named producer with a bigger footprint (none used yet):
  # star-history:     { type: star-history }
    # ── contents · a build-manifest section, build referenced by id · view: badges|table|list|kv|versions ──
    contents-base:    { type: contents, topic: inventories.versions, build: stagefreight, view: badges }
    contents-apk:     { type: contents, topic: inventories.apk, build: stagefreight,      view: badges }
    # ── include · inject a docs fragment ───────────────────────────────────────────────────────
    cli-reference:    { type: include,  path: docs/assets/modules/cli-reference.md }
    config-reference: { type: include,  path: docs/assets/modules/config-reference.md }

  # ── Placement: verbs only (between/mode/inline). items are pure name refs + `br`; zero creation ──
  files:
    readme.badges:
      file: README.md
      between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
      mode: replace
      inline: true
      items: [build, license, release, updated, donate, sponsor]

    readme.project:
      file: README.md
      between: ["<!-- sf:project:start -->", "<!-- sf:project:end -->"]
      mode: replace
      inline: true
      items: [github, gitlab, go-report, go-reference, last-commit, issues-open, prs-open, contributors]

    readme.image:                                    # not inline — `br` forms the rows
      file: README.md
      between: ["<!-- sf:image:start -->", "<!-- sf:image:end -->"]
      mode: replace
      items: [docker, pulls, br, release-latest, release-updated, release-size, dev-latest, dev-updated, dev-size]

    readme.contents.base:
      file: README.md
      between: ["<!-- sf:contents-base:start -->", "<!-- sf:contents-base:end -->"]
      mode: replace
      items: [contents-base]

    readme.contents.apk:
      file: README.md
      between: ["<!-- sf:contents-apk:start -->", "<!-- sf:contents-apk:end -->"]
      mode: replace
      items: [contents-apk]

    cli-reference:
      file: docs/reference/CLI.md
      between: ["<!-- sf:cli-reference:start -->", "<!-- sf:cli-reference:end -->"]
      mode: replace
      items: [cli-reference]

    config-reference:
      file: docs/reference/Config.md
      between: ["<!-- sf:config-reference:start -->", "<!-- sf:config-reference:end -->"]
      mode: replace
      items: [config-reference]

  commit:
    type: docs
    message: "refresh generated docs and badges"
    add: ["README.md", "docs/assets/modules", "docs/reference", ".stagefreight/badges"]
    push: true
    skip_ci: true

build_cache:
  mode: hybrid
  external: { registry: harbor, path: cache, fallback: main }   # was target: harbor-dev → reference the registry directly (it has url + default_path)
  local:
    retention: { max_age: "7d", max_size: "8GB" }               # cap builder cache below the 15GB default

commit:                                                          # global commit defaults
  default_type: docs
  conventional: true
  backend: git

test:
  enabled: true
  suites:                                                        # keyed by id
    unit: { tool: go, packages: [./...], race: true, coverage: true, gate: perform }

dependency:
  enabled: true
  output: ".stagefreight/deps"
  scope: { go_modules: true, dockerfile_env: true }              # a named set — stays a map (configurable)
  commit: { enabled: true, type: chore, message: "update managed dependencies", push: true, skip_ci: false }

lint:
  level: full
  exclude: ["*.ttf", "*.png", "*.jpg", "*.ico", "*.woff", "*.woff2"]
  modules:                                                        # each a map (configurable); the pointless `options:` wrapper is gone
    freshness:
      enabled: true
      cache_ttl: 300
      sources: { docker_images: true, github_releases: true, go_modules: true }   # named set — keep nested
      vulnerability: { enabled: true }                                            # sub-feature — keep a map
    tabs:          { enabled: true }
    direct-output: { enabled: true }    # enforce the diag/renderer boundary on StageFreight's own source
    conflicts:     { enabled: true }
    filesize:      { enabled: true }
    secrets:       { enabled: true, exclude: [go.sum] }
    unicode:       { enabled: true, detect_bidi: true, detect_zero_width: true, detect_control_ascii: true }

security:
  enabled: true
  output: ".stagefreight/security"
  sbom: true
  release_detail: full

release:
  enabled: true
  security_summary: true              # attach it; location = security.output (was a duplicated path)
  registry_links: true

toolchains:                            # name → desired version (`desired:`/`version:` wrappers dropped; use a map form per-tool for sha/url)
  trivy: "0.69.3"
  syft: "1.42.3"
  grype: "0.110.0"
  osv-scanner: "2.3.5"
  cosign: "3.0.6"
  flux: "2.8.3"
  kubectl: "1.34.2"
```
