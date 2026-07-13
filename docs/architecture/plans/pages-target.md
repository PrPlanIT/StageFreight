# Design: `kind: pages` — deploy static sites to Cloudflare / GitHub Pages

Status: **proposed** (not yet implemented). Owner: build/targets.

## Motivation

Host static sites — **documentation for many apps** and **UX demos of other apps** —
built from their source repos and fanned out to a Pages host. This offloads the
self-hosted static-site containers currently running in k8s (or runs alongside them),
and gives every app's docs/demo a public URL that redeploys **only on versioned
releases, never on dev pushes**.

There is no prior plan for this. `docs/config/11-static-site.yml` is a docs-repo
*archetype* (release notes + dependency scanning, `Build: None`) — it does not build
or deploy a site. Target kinds today are `release`, `registry`, `binary-archive`,
`generic-package`, `docker-readme`. `pages` is a new kind.

## Shape: build vs. deploy

A static site is a **folder of files**. Two separable concerns:

- **Build** (existing): produce the folder. The containerized build engine already
  emits a **directory tree** (`dist/`, `public/`, `site/`, `_site/`). Docusaurus is
  `builder: node` today; Hugo/Jekyll/MkDocs would be a `builder: static` convention
  that detects the generator (`hugo.toml`→hugo, `mkdocs.yml`→mkdocs, `_config.yml`→
  jekyll, `docusaurus.config.js`→node) and knows its output dir — the same
  detect-the-tool pattern as `builder: c`. *No new deploy machinery.*
- **Deploy** (new): ship that folder to a Pages host. This is the `pages` **target**.

The target model (`CollectTargetsByKind`) is the seam; `pages` slots in beside
`binary-archive`.

## Cloudflare-first

Both providers are free. The deciding factors favor Cloudflare for this use case,
especially a **central, many-apps docs hub**:

| | Cloudflare Pages | GitHub Pages |
|---|---|---|
| Bandwidth | unlimited | ~100 GB/mo soft cap |
| Custom domain DNS | **auto-wired** (domain already on CF) | manual CNAME record (yours) |
| PR previews | **built-in**, ephemeral | not native |
| Deploy rate | monthly budget (~500/mo), no hourly wall | **~10 builds/hr per site** |
| Base path | serves at domain **root** | project pages at `/<repo>/` |

The ~10/hr GitHub limit is *per site* — fine for one project, but the fan-in
many-apps→one-hub pattern can trip it. Cloudflare's monthly budget absorbs bursts.
(These limits drift; verify current figures before depending on them.)

**Default `provider: cloudflare`; GitHub is the secondary provider**, with its
per-hour limit and manual-DNS caveat documented so nobody wires a busy hub to it.

## Config contract

Reasonable defaults + escape hatches; symmetric with existing targets. Not a
scripting surface.

```yaml
targets:
  - id: docs
    kind: pages
    provider: cloudflare          # default; or github

    # ── source: what folder ships ───────────────────────────────
    build: site                   # a builds[].id whose tree we publish
    # dir: ./public               # …or publish a repo folder directly (no build)
    # sources:                    # …or merge several, each mounted at a path
    #   - { build: site }
    #   - { dir: demos, to: /demos }
    include: ["downloads/*.pdf"]  # extra repo assets merged in (expandSource + glob)
    exclude: ["**/*.map", "**/*.draft.html"]

    # ── destination + wiring ────────────────────────────────────
    domain: docs.myapp.com        # CF: auto-DNS. GH: writes CNAME into the tree.
    base_path: /                  # inferred: CF→"/", GH project→"/<repo>/".
                                  #   FED INTO the build (generators need it).
    # project: docs-hub           # CF project (default: repo name)
    # repo: myorg/docs-hub        # GH destination repo (default: this repo)
    # branch: gh-pages            # GH branch (default: gh-pages)

    # ── when: release-gated, not dev spam ───────────────────────
    when: { git_tags: ["re:v.*"], events: [tag] }
    # …or split environments:
    # environments:
    #   production: { when: { git_tags: ["re:v.*"], events: [tag] } }
    #   preview:    { when: { events: [pull_request] } }   # throwaway CF preview

    # ── versioning (see below) ──────────────────────────────────
    # versioning: { mode: replace }        # default
    # versioning: { mode: keep, alias: latest, retain: 10, index: true }
```

### The base_path coupling (important)

GitHub *project* pages serve at `/<repo>/`, not root — a site built for `/` 404s
every asset there. So `base_path` is **not independent of the build**: the target's
base path is an *input to the builder* (`baseurl`/`base`/`--base`). The pages target
must surface its resolved base path to the build it references. Cloudflare at a domain
root sidesteps this (`base_path: /`), which is another reason to default to it.

## Secrets

Provider credentials use the **`ForwardEnv`** mechanism (built for android keystores):
named host env vars (CI secrets) forwarded into the deploy step by value.

- Cloudflare: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`
- GitHub: `GITHUB_TOKEN` (or a deploy key)

## Deploy mechanism

- **Cloudflare:** `wrangler pages deploy <dir> --project-name <project>` run in a node
  container (reuse the container engine). Direct-upload of a pre-built folder — does
  not consume CF's git-build budget. Production vs. `--branch` preview.
- **GitHub:** commit the tree to `<branch>` (default `gh-pages`) in `<repo>` and push.
  SF already does git natively. A `CNAME` file is written into the tree when `domain:`
  is set.

## Versioning

Two models, a `versioning.mode` knob; **`replace` is the default.**

1. **`replace`** — each release overwrites; the site shows the latest release. Simple,
   the common case for product/marketing docs.
2. **`keep`** — every released version stays browsable (`/v2.1/`, `/v2.0/`, `/latest/`).

There are **two ways to achieve `keep`**, and one is nearly free:

- **Generator-owned (preferred).** Docusaurus (`versioned_docs/`) and MkDocs (`mike`)
  emit a *complete multi-version site in one build* — picker and all. SF just does a
  normal **`replace`** deploy of that already-versioned build. **No SF versioning code.**
  This is the battle-tested path; prefer it whenever the generator supports it.
- **SF-orchestrated (`mode: keep`, phase 2).** For generators without versioning (or
  plain HTML). A Pages deploy is an atomic full-site replace, so keeping old versions
  means SF assembles the whole tree each release. Old versions come from the
  **retained per-release site artifacts** (SF already archives release build outputs) +
  the new build — not from scraping the live site. This owns `alias` (`/latest/`),
  `retain`/prune (last N), and the `index:` version-picker page.

## Reused primitives (why this is small)

- **tree artifacts** — the built site is a directory; already captured + archived.
- **`expandSource`** — walks a directory into entries; `include`/`exclude` is the same
  `IncludeFiles` + glob pattern binary-archive uses. Asset selection is *not* new code.
- **`ForwardEnv`** — deploy secrets (android keystore mechanism).
- **container engine** — runs `wrangler`.
- **`when:` gating** — release-gated deploys, existing mechanism.
- **`{version}` templating** — versioned paths (already in the engine).

## Phasing

- **Phase 1** — `kind: pages`, Cloudflare provider, `replace`, release-gated,
  `build`/`dir`/`sources` + `include`/`exclude`, `domain` + auto-DNS, CF previews.
  Generator-owned versioning works here for free (it's just a `replace` of a versioned
  build). GitHub as secondary provider with its caveats documented.
- **Phase 1.5 (parallel, optional)** — `builder: static` SSG-detection convention, so a
  docs repo is just `builder: static` + `kind: pages`.
- **Phase 2** — SF-orchestrated `versioning: keep` (retain/prune/`latest`/index),
  deferred until a repo whose generator can't version actually needs it.

## Open decisions / risks

- **base_path → build coupling** — the resolved base path must reach the referenced
  build. Needs a clean mechanism (target annotates the build, or the build reads a
  known var). The one genuinely cross-cutting piece.
- **GitHub per-hour limit** — document it on the GitHub provider; steer hubs to CF.
- **Untestable until real** — like electron/wine and android, the deploy path can only
  be fully verified against a real CF/GH account + domain. First real deploy validates.
- **`include`/`exclude` relativity** — defined as repo-root-relative; each source's
  `to:` sets its mount point in the published tree (default root).
```
