# `.stagefreight.yml` shape proposals — scratchpad

Working doc to iterate the config surface (starting with `narrate`) toward something
**tidier and compose-ergonomic without losing any capability**. Nothing here is
committed to the schema yet — it's a design scratchpad. The live `.stagefreight.yml` is
the reference for "what must still be expressible."

> ### ⚙️ Presets — ALREADY BUILT (reminder, don't redesign the mechanism)
> Every top-level key is preset-able; this is implemented, not aspirational. Anchor to the code, not to guesses.
> - **Shape (per-section, not whole-file):** `preset: <path>` on any section (a `preset` key inside the section map; local siblings override) · `presets: [<path>…]` for **ordered compose**, allowed only on keyed-collection sections (`targets`/`builds`/`badges.items`/`versioning.tag_sources`/`versioning.branch_builds`/`narrator`). A preset file declares **exactly one** top-level key, matching the section it imports into. `preset:`+`presets:` on one section = error. (`src/config/preset.go:35-42,449-503`)
> - **Call-back (resolution):** `PresetLoader.Load(path)` — resolves from **local FS relative to the config** (satellites read the on-disk cache) **or remote git-clone of the policy repo by PINNED ref** (SSH/HTTPS; branch refs need `allow_floating`). Cycle + path-traversal guards. No HTTP-URL / forge-REST preset fetch. (`src/config/preset.go:12-14,50-247` · `src/governance/loader.go:22-154`)
> - **Cache:** on-disk **git-committed** `.stagefreight/preset-cache/` in each satellite — governance distribution writes 1:1 copies; **runtime resolves from the committed cache** (reproducible, no live fetch at build time). `preset_source:` block carries forge coords + `cache_policy: authoritative`; refresh = governance re-run (per-file drift). `vars` presets are pre-resolved to concrete values, not cached as refs. (`src/governance/distributor.go:29-105,188-237` · `src/paths/paths.go:10`)
> - **Merge:** `DeepMerge` — maps deep-merge recursively; **scalars & lists replaced** wholesale by override (override wins); `presets:` list = append/compose + **dedup by id**. Per-value provenance is recorded (`MergeEntry`: set/override/replace/merge/append) → this IS the resolved-view provenance we wanted. (`src/config/preset.go:518-543,22-31`)
> - ⚠️ **NEEDS-CODE / verify:** the main runtime load `LoadWithWarnings` (`src/config/config.go:128-165`) does **not** call `ResolvePresets`; the documented `loadResolved` "resolve-before-decode" invariant (`docs/design/invariants.md:19-40`) has **no implementing symbol**. Resolution today runs only in `config resolve`, the provenance reporter, and governance distribution. Confirm presets actually resolve on the build/run path before treating preset-bearing sections as runnable.

> ### 💳 Creds — insight (a cred is a placeable *reference*, never a value)
> A credential is **how you auth to a targetable featureset** — orthogonal to *what* you target (identity layer models featuresets, not vendors; the shared token is a `secrets:` atom two featuresets happen to point at). So a cred is a `credentials: <secret-name>` **reference**, placeable at **any scope** — global default → per-featureset (`forges`/`registries`) → per-target/per-repo — **nearest-wins**. The **value** always resolves through `secrets:` (env/sops/vault); never inline in config.
> - **Why any-level:** one mechanism serves both org shapes — org-token shops set it once high; per-project-token shops (token sprawl) drop the ref at the target/repo level, where their token boundaries already are. Same nearest-wins override as [[presets]], applied to one attribute — no new machinery.
> - **Ref, not value:** placement is flexible; the secret stays in the secrets layer. No plaintext tokens scattered across the config.
> - **Provenance is mandatory:** the resolved/`plan` view must report *which scope* each cred resolved from — the token-sprawl orgs this serves care most about "which token touches which target" (blast radius, rotation).
> - **Bonus — token-topology map:** cred-ref + provenance = "show me every credential and every featureset/target it authorizes" falls out for free (a query over the resolved config; ties to the audit-log/event-substrate work). Answers the *management* pain, not just expression.
> - **Signing stays explicit:** safety-critical creds (signing keys) are stated, not silently inherited from a broad default — consistent with presence-to-disable-must-be-explicit.

> ### 🎭 narrate — RESOLVED (reconciler) + PROPOSED (split commit out; narrate = the run's story)
> **Resolution (ordering solved).** narrate is a **reconciler**, not a phase-at-time-T. Declare the input→render→target graph at load; **re-render a target whenever one of its inputs changes**, where inputs = **workspace files ∪ pipeline-state tokens** (`{env:BUILD_STATUS}`, `{docker.pulls}` are *state*, not files — watching state-as-input is what fixes the status badge). Transitive: a generated include (docsgen → `cli-reference.md` → README-include → docs-site) cascades downstream, make-style. Idempotent → converges to a fixpoint; acyclic (a cycle is a config error); **cheap** (watch only the graph's files + declared state-tokens; re-render only the affected subgraph). A build/action's **declared file-outputs** are the edges that pull non-narrate actions into the graph — and double as the preview-tree + audit line. Ordering dissolves into dependency; no manual order needed. (Tracked: #39.)
> **Proposed reframe (under discussion).** Today's `narrate: { props, files, commit }` conflates **three** responsibilities: *scribe* (props+files — render content into workspace files via markers/regex; **keep as-is, it's the good part**), *flush* (the git commit — actually **generic**, not narrate-specific), and *report* (the run's story — barely present today). Split them:
> - **flush → a terminal workspace-write** (after publish, "update primary-origin"): the definitive, trustworthy commit of *all* workspace mutations back to git, with full success-knowledge up to the write; may emit **classified** commits (docs `skip_ci` vs source). Its manifest lists which props embedded into which docs (from the declared-mutations record).
> - **narrate → the run's report**: summary / notifications / alerts / HUD — **SF-templated or LLM-generated** (ollama / anthropic / openai) — "YAY we shipped" or "oh-shit, here's what broke" at a glance. Configurable. The **human layer over the event substrate** (#38); absorbs announcements (#35). narrate finally *means* narrate.
> - **Why the flush is evicted (the load-bearing reason, not hygiene):** a reporter must not own any mutation that can *fail* — **a reporter that can fail can't be trusted to report failure.** The commit is the one fallible thing in narrate (rejected push, dirty tree, auth); leave it in and a bad push kills the reporter exactly when there's a failure to report. So narrate is made **mutation-free** — it mutates *nothing the run depends on* (no workspace, no repo, no run-state), only *outbound, fail-soft* effects (notifications/logs; a Slack outage never fails the run) — which makes it an **unkillable `finally`** that always runs. Everything fallible (scribe's writes, the flush) runs *before* it; narrate is pure *read → outbound report*. (Same property is why an LLM summary is safe here: post-commit, outbound, fail-soft.)
> - Status badge = *scribe* (reflects outcome up to the flush); run notification = *narrate* (reflects the total, incl. the flush) — resolves the badge chicken-egg cleanly (a badge can't know the success of the commit it's inside).

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

> **⚠️ SUPERSEDED.** Field-inference (`kind:` derived from which payload field is present)
> was rejected: an explicit discriminator ages better and stays extensible (`type: badge`
> today → `type: markdown|html|svg|graph` tomorrow without the field-set going ugly). **The
> final config uses an explicit `type:` on every prop.** This proposal is kept only as the
> historical reasoning; do not implement the inference.

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
lifecycle: image                         # root mode selector: image | gitops | governance | docker (experimental)
# This config is the IMAGE mode. Structure = SHARED clusters (present in every mode) + ONE mode-specific pipeline:
#   shared:        ci · vars · git · forges/repos/registries/signing · commit/tagging/release/glossary · narrate · lint/test/security/dependency · toolchains · manifest · defaults
#   mode-specific: image → builds + publish + sync    ·    gitops → gitops:    ·    governance → governance:    ·    docker → docker: (experimental)
#   (a gitops config, e.g. SoFMeRight/dungeon, drops builds/publish/sync and carries gitops: instead — same shared clusters)
# description + license auto-detected — license ← LICENSE file ({project.license}), description ← publish-origin repo. Declare at root only to override.

ci:                                      # runner block — image + routing (cohesive; image un-hoisted from root)
  image: docker.cr.pcfae.com/prplanit/stagefreight:latest-dev   # per-build image: overrides
  # routing: {}                          # optional: per-phase runner placement → GitLab tags / GitHub runs-on

vars:
  org: prplanit
  github_org: PrPlanIT
  gitlab_group: PrPlanIT
  repo: stagefreight
  github_repo: StageFreight

git:                                     # interpret the ref → named patterns + the versions they imply (was matchers + versioning)
  branches:                              # order-free named lookups (matchers) → MAP
    main: "^main$"
  tags:                                  # MAP, order-free — patterns are MUTUALLY EXCLUSIVE (a tag matches at most one); overlap = config error, not a first-match tiebreak. NEEDS-CODE: engine today does declaration-order first-match.
    stable:     { pattern: "^v?\\d+\\.\\d+\\.\\d+$" }
    prerelease: { pattern: "^v?\\d+\\.\\d+\\.\\d+-.+" }
  versioning:                             # derivation rules that consume the patterns above
    branch_builds:                       # MAP, order-free — `default` is the NAMED catch-all (named rules match by branch; `default` is the fallback), NOT a positional last. NEEDS-CODE: engine today requires default-last.
      default: { base_from: [stable], format: "{base}-dev+{sha}" }   # `format` produces {version} off-tag
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

manifest:                                # build-evidence data bus (builds → narrate); default-off, on when consumed — presence = enabled
  mode: commit                           # ephemeral | workspace | commit | publish
  output_dir: .stagefreight/manifests
# defaults: {}                           # reserved, engine-ignored slot for user &yaml-anchors (rename candidate: anchors:)

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

signing:                                 # signing subsystem — operational switch + trust profiles (was signing + signing_profiles, merged)
  enabled: true                          # explicit on purpose — never presence-enable minting a trust identity
  auto_provision: false
  state_dir: { type: volume, name: sf-signing }
  profiles:                              # keyed by id; a publish target references one via signing_profile: <id>
    release:  { requires: keyless,  transparency_log: true }
    hardware: { requires: hardware, physical_presence: true, non_exportable: true }

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
  registry-meta:                                     # push project metadata; description (short, from publish-origin) + readme (long)
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

narrate:                                 # renders props into files, then commits (terminal phase). NOTE: ideal render→consumer ordering (docs-site/readme/pages read narrate's files; the build-status badge needs the FINAL outcome; a private CI can't be a live badge) is a RUNTIME data-availability problem — early build-time wave vs late status wave — TBD in the engine, NOT a schema-shape concern.
  # ── props: TWO orthogonal axes — SOURCE (the data, `type:`) × RENDER (the form, `render:`). Placement stays in files:. ──
  #    SOURCE = `type:` names the producer; OMIT it for inline (you compose the verbs below). No provider:/topic:/host.
  #      inline · goreportcard · go-reference · github-last-commit · github-issues-open · github-* · contents · include · text · component · k8s-inventory · star-history
  #    RENDER = `render:` names the form: badge (DEFAULT) · shield · image · table · list · kv · versions · raw   (later: markdown · html · json)
  #      inline → badge|shield (you pick) · data producers (github count/date) default a form, accept others ·
  #      fixed-form producers (goreportcard → image) FORBID render: — an ignored knob is a lie → validation error.
  #    STRUCTURAL sources keep their own coordinates: contents = build: + section: · include = path: · component = ref: · k8s-inventory = live cluster (gitops)
  #  BADGE areas (inline), one verb each: logo/label/message (icon/left/right) · logoColor/labelColor/color · link · font/font_size (local render only)
  #  label: defaults to the prop's KEY when omitted (build → "build"). State it to differ (github → "GitHub"), to
  #    reuse a display label across uniquely-keyed props (release-updated & dev-updated both "updated"), or when the
  #    text would break YAML / force a duplicate key. Both forms are accepted; examples below always encode explicit.
  props:
    # ── inline source → render: badge (the DEFAULT — SF resolves {vars}, draws the SVG locally) ──
    build:            { label: build,      message: "{env:BUILD_STATUS}",           color: auto,      font: monofur,                 link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}/-/pipelines" }
    license:          { label: license,    message: "{project.license}",            color: "#310937", font: monofur,                 link: LICENSE }
    release:          { label: release,    message: "v{base}",                      color: "#74ecbe", font: dejavu-sans, font_size: 11, link: "https://github.com/{var:github_org}/{var:github_repo}/releases" }
    updated:          { label: updated,    message: "{env:BUILD_DATE}",             color: "#236144", font: dejavu-sans, font_size: 11 }
    pulls:            { label: pulls,      message: "{docker.pulls}",               color: "#1d63ed",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}" }
    release-latest:   { label: latest,     message: "v{base}",                      color: "#74ecbe",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest" }
    release-updated:  { label: updated,    message: "{docker.tag.v{base}.updated}", color: "#236144" }
    release-size:     { label: size,       message: "{docker.tag.v{base}.size}",    color: "#555",                                   link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=v{base}" }
    dev-latest:       { label: latest-dev, message: "dev-{sha:8}",                  color: "#3b82f6",                                link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest-dev" }
    dev-updated:      { label: updated,    message: "{docker.tag.latest-dev.updated}", color: "#236144" }
    dev-size:         { label: size,       message: "{docker.tag.latest-dev.size}", color: "#555",                                   link: "https://hub.docker.com/r/{var:org}/{var:repo}/tags?name=latest-dev" }
    # ── inline source → render: shield (shields.io draws it from the same composed verbs; no %2F juggling) ──
    donate:           { render: shield, message: donate,  color: "#FF5E5B", logo: ko-fi,          logoColor: white, link: "https://ko-fi.com/T6T41IT163" }
    sponsor:          { render: shield, message: sponsor, color: "#EA4AAA", logo: githubsponsors, logoColor: white, link: "https://github.com/sponsors/{var:github_org}" }
    github:           { render: shield, label: GitHub, message: source, color: "#181717", logo: github,                  link: "https://github.com/{var:github_org}/{var:github_repo}" }
    gitlab:           { render: shield, label: GitLab, message: source, color: "#FC6D26", logo: gitlab,                  link: "https://gitlab.prplanit.com/{var:gitlab_group}/{var:repo}" }
    docker:           { render: shield, label: Docker, message: "{var:org}/{var:repo}", color: "#2496ED", logo: docker, logoColor: white, link: "https://hub.docker.com/r/{var:org}/{var:repo}" }
    # ── named producers · type: = the SOURCE — self-contained; repo/module resolve from repos: (override repo: <id>) ──
    go-report:        { type: goreportcard }                    # render: image (fixed — setting render: is an error)
    go-reference:     { type: go-reference }                    # render: image (fixed)
    last-commit:      { type: github-last-commit }              # render: shield (default; a count/date — render: badge|table also valid)
    issues-open:      { type: github-issues-open }
    prs-open:         { type: github-prs-open }
    contributors:     { type: github-contributors }
  # star-history:     { type: star-history }                    # block widget · render: image — none used yet
    # ── contents · a build-manifest section (build by id) · render: badges|table|list|kv|versions ──
    contents-base:    { type: contents, build: stagefreight, section: inventories.versions, render: badges }
    contents-apk:     { type: contents, build: stagefreight, section: inventories.apk,      render: table }
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

  commit:                                # narrate's OWN terminal persist (like dependency.commit) — gated all-green, runs AFTER the render's consumers
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

# ── change-narrative cluster: author what a release SAYS (commit/tagging/release), rendered per surface ──
commit:                                                          # commit authoring + render (was commit + presentation.commit)
  default_type: docs
  conventional: true
  backend: git
  render: { preserve_raw_subject: true }

tagging:                                                         # tag-CREATION policy (was `tag:`; renamed to disambiguate from registry & git-tag patterns)
  target: HEAD
  preview: true
  require_approval: true
  push: true
  message: { mode: prompt_if_missing, empty_strategy: prompt }
  render: { max_entries: 20, group_by_type: true, style: concise }

glossary:                                                        # change-language vocabulary (commit types, aliases, breaking detection) — shared by commit/tagging/release
  types:
    feat:  { release_visible: true }
    fix:   { release_visible: true }
    chore: { aliases: [build, ci], release_visible: false }
  breaking: { bang_suffix: true, footer_keys: ["BREAKING CHANGE"] }

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

release:                              # release assembly + render (change-narrative release surface); the publish `kind: release` target ships it
  enabled: true
  security_summary: true              # attach it; location = security.output
  registry_links: true
  render: { max_entries: 50, group_by_type: true, style: explanatory }

toolchains:                            # name → desired version (`desired:`/`version:` wrappers dropped; use a map form per-tool for sha/url)
  trivy: "0.69.3"
  syft: "1.42.3"
  grype: "0.110.0"
  osv-scanner: "2.3.5"
  cosign: "3.0.6"
  flux: "2.8.3"
  kubectl: "1.34.2"
```
