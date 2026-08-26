# StageFreight — Identity Model & Governance (design of record)

> **Status: proposed / not-yet-built.** This is a design-of-record, not a description of
> shipped behavior. Only *Deliverable 0* (the `loadResolved` cache path) exists in code today,
> and it is reframed below. The invariants in §10 are **candidates** — each graduates into
> [`invariants.md`](invariants.md) only once it is enforced in code (per that file's rule:
> "an invariant that lives only in a file is a wish"). Worked examples that track this spec live
> outside the repo in `stagefreight-scratchpad/{1-MaintenancePolicy,2-prometheus-eaton-ups-exporter,3-native-apt-cacher-ng}.stagefreight.yml`.

---

## 0. Why this exists

Standing up a governance control repo exposed that StageFreight has **no first-class notion of a
project's identity**. A project's name is re-typed per surface as hand-maintained vars; license is
a fragile `LICENSE` scan; description is inline on publish targets; and shared build recipes hard-code
repo-specific literals (e.g. a Go module path in ldflags), so one preset cannot serve a bucket of
repos. The result is drift that must be hand-managed across the fleet.

This model makes identity **declared once, read everywhere**, and reframes governance as *authority
over shared facts* rather than a second configuration language.

## 1. Thesis — what is a repository

A repository has: an **authoritative location**, an **org**, **identity/branding**, **derived surface
paths**, **policy**, optional **local overrides**, and **lifecycle behavior**. Those facts mean the
same thing whether the repo declares them itself or a catalog is authoritative about them.

> `metadata` / `orgs` / `{path.*}` / aliases are **StageFreight concepts, not governance concepts.**
> Governance is one *authority* that supplies them: "here are the facts and policies these repos would
> have declared themselves, except I'm authoritative about them."

The proof is a trio at three authority levels with identical semantics: **policy repo → governed
satellite → fully native repo** (scratchpad files 1 → 2 → 3).

## 2. Primitives

| Section | Role |
|---|---|
| `orgs:` | Identity only — `maintainer` + open `aliases` map. **No forge, no credentials.** |
| `metadata:` | Per-repo identity/branding — the one block every consumer reads. |
| `forges:` / `registries:` | Surfaces. Arbitrary id, `provider` is a field, each carries a `default_path` template. |
| `repos:` | Where source-of-truth lives + publish routing; the **primary** entry is the location anchor. |
| `governance.stacks:` | The catalog + shared policy (an authority that supplies the above to satellites). |

Everything mechanical (`{path.*}`, `{project.module}`, `{org.*}`, `{slug}`) is **derived**, never declared.

## 3. Location anchoring

> **A repository's identity derives from its primary location; the location is the single fact.**

- **Native:** the anchor is `repos.primary.project` (e.g. `HomeLabHD/apt-cacher-ng`, on `forge: gitlab`).
- **Catalog:** the anchor is the entry's `at:` (or the bare-string form `id: HomeLabHD/apt-cacher-ng`),
  where an optional `<forge>:` prefix defaults to the stack's primary forge.

From the anchor, **`org` and `slug` derive**: the location's group names an `orgs:` entry (→ its aliases);
the last path segment is the slug. `{path.<primary-forge>}` *is* the anchor; every other surface path
derives from `(org, surface.default_path, names[surface] ?? slug)`.

**Why:** deriving location from a declared `org` + `slug` lets a config *contradict itself*
(`org: A` + `at: B/…`). Anchoring on the location makes contradiction unrepresentable — there is one
fact and everything is a consequence. An explicit `org:` override is allowed only for the rare case
where the location's group ≠ the org id.

## 4. Derived facts

| Fact | Derivation |
|---|---|
| `{org}` | The current repo's org id (from the anchor). |
| `{org.lower}` / `{org.<alias>}` | Lowercased id / a named alias from `orgs.<id>.aliases` (open map). |
| `{orgs.<id>.…}` | A specific org by id (for cross-org references). |
| `{slug}` | The anchor's last path segment (build `output`, path name default). |
| `{path.<surface>}` | `surface.default_path` resolved with `{org.*}` + `names[surface] ?? slug`. One rule across forges and registries. |
| `{project.module}` | Canonical module/package name, **builder-dispatched**: `go.mod` module / `Cargo.toml` package / `package.json` name / `pyproject` project. |
| `{project.since}` | First-commit date (already a project fact). |
| `{metadata.*}` | Fields of the `metadata` block (`{metadata.title}`, `{metadata.license}`, `{metadata.description}` = the default's shortest tier, …). |

### 4.1 Resolution order (mandatory)

These facts are **interdependent** — `{path.dockerhub}` → `registries.dockerhub.default_path` →
`{org.handle}` → the anchor. A single flat substitution pass leaves a token that expands into another
token half-resolved. Therefore:

> Derived facts resolve in **topological/dependency order** and are frozen before the leaf template pass:
> (1) `{org.*}` from the anchor + `orgs`; (2) each surface `default_path` using those; (3) `{path.*}`
> from the resolved `default_path`s + `names`; (4) `{metadata.*}`, `{project.*}`.

This ordering is owned by the shared fact resolver (§9).

### 4.2 `{project.module}` — the headline

The build args already template-expand (`{version}`/`{sha}`/`{date}`). The only thing stopping one
preset from serving a whole bucket is a repo-specific literal. `{project.module}` removes it:

```yaml
# preset/build-go-binary.yml — serves stagefreight, hasteward, polysieve, …
args:
  - "-ldflags"
  - "-s -w -X {project.module}/src/version.Version={version} \
            -X {project.module}/src/version.Commit={sha} \
            -X {project.module}/src/version.BuildDate={date}"
```

Because it is builder-dispatched, `build-rust-binary.yml` collapses the same way (`Cargo.toml`).
**Limit:** it makes the *identity/coordinate* portion shareable; it cannot reconcile genuinely divergent
internal layouts — a bucket sharing a preset agrees on convention (version package path, entrypoint,
output == slug), or a divergent segment becomes its own var.

## 5. Scoped values — `description` and `readme`

Surfaces differ by **slot** (a short one-liner slot; a long-form README slot), not by wanting different
*text*. So a value is either the inferred **default** or **scoped per surface** — the same override
idiom as `names`.

> **A named surface accepts exactly one value; only the *default* may be tiered.**

```yaml
# plain = default only (the common case)
description: "Rootless apt cache that de-dupes apt downloads fleet-wide."

# scoped = tiered default + single-string named overrides
description:
  default:                                   # StringOrList — tiered, fans out to un-named surfaces
    - "Rootless apt cache."
    - "Rootless apt cache that de-dupes apt downloads fleet-wide; env-tunable retention."
  dockerhub: "Rootless apt cache · docker run hlhd/apt-cacher-ng"   # ops audience, ONE string
  github:    "Caching-proxy container · GPL-2.0 · PRs welcome"      # dev audience, ONE string
```

**Resolution at surface S:** `value[S] ?? value.default`, then (for `description`) fit-pick the default's
tier to S's slot. Tiering lives exactly where fan-out happens (the default) and is a **hard error** where
it is meaningless (a list under a named surface). `readme` is the sibling with no tiers (one long-form
slot everywhere): default path or per-surface paths, all scalars.

The `kind: metadata` publish target carries **no identity fields** — only `registry`/`repos`/`when`
(where to push). *What* it pushes (readme included) lives in `metadata:`.

## 6. Surfaces

Every surface (forge or registry) has: an arbitrary `id` (yours), a `provider` field, a `url`,
a `credentials` reference, and a `default_path` template resolved via `{org.*}`:

```yaml
forges:
  gitlab: { provider: gitlab, url: "…", credentials: GITLAB, default_path: "{org}/{repo}" }
registries:
  dockerhub: { provider: docker, url: "docker.io", credentials: DOCKER, default_path: "{org.handle}/{repo}" }
```

Per-org group nesting (e.g. GitLab subgroups) comes from an org alias referenced in `default_path`
(`default_path: "{org.gitlab_group}/{repo}"`), so nesting lives on the org where it varies, not the
shared forge.

## 7. Preset resolution — source-tracking, cache-as-fallback

> **A `preset:` reference is a live *link to its source*; the preset-cache is a fallback/pin store,
> never the mandatory read path.**

This is what makes governance a *tracked authority* rather than a re-run migration.

**Reference forms** (`preset:` value):
- **local** — `preset/lint.yml`, relative to the repo (working-tree read).
- **sourced** — `<source>//<path>[@<ref>]`. `<source>` = forge repo or URL; governance-written satellite
  refs point at `governance.source`, inheriting its repo + ref.
  - **tracked** (`@<branch>` or no ref) → **fetched live each run**; success refreshes the cache; fetch
    failure falls back to the retained copy (warn, don't fail). *Default; the "change once, fleet
    follows" path.*
  - **pinned** (`@<tag>`/`@<sha>`) → **cache-authoritative / immutable**: cached copy if present, else
    fetch once and cache. Reproducible + offline-safe.

**Resolution never requires committed presets — dry-run or real.** Because source-fetch is primary, a
repo that has never been reconciled (only a `governance.source` pointer, or a bare foreign
`url//name.yml`, and *no* committed cache) still fully resolves and previews: `config render`,
`governance reconcile --dry-run`, and audition all fetch from source. Governance `--dry-run` resolves
each satellite from source without writing.

**Failure semantics (must be honest):** pinned + offline → resolve from cache; tracked + offline + stale
cache → fall back and warn; tracked + offline + no cache → **fail clearly**, never silently.

**Deliverable 0 reframed.** The shipped `loadResolved` change points `localPresetLoader.baseDir` at
`<root>/.stagefreight/preset-cache/` when present (`src/config/config.go`; `dirExists` in
`src/config/report.go`). That makes the cache the *primary* path — **backwards** under this model. It is
retained as the **fallback/pinned** branch; the tracked-ref primary is a source-fetch loader added
alongside it.

## 8. Governance model

`governance.stacks` is presence-gated: the presence of the section activates reconcile — **no
`lifecycle.mode: governance`** (runtime and emitter already gate on `len(governance…) > 0` at
`src/ci/facets.go` and `ci_runners.go`). A satellite instead carries `governance.source`
(`repo_url` + `ref`).

Each **stack** = `config` (shared presets; its `repos` preset's `primary` role names the write forge +
default) + `repos` (the catalog, an id-map).

**Catalog entries are location-anchored** (§3) and govern identity **per entry, presence-gated:**

- **Bare-string entry** (`some-tool: HomeLabHD/some-tool`) → catalog governs **CI only**; the repo
  authors its own `metadata:` **locally** (the two-file merge takes the local block because the managed
  config carries none).
- **Branded entry** (`at:` + `title`/`description`/…) → catalog governs **identity wholesale**; a
  competing local `metadata:` block is a **hard error** ("make the entry location-only to author
  locally"). No two authors, no list-merge ambiguity.
- Optional per-repo **`config:`** override on an entry deviates from shared policy for that repo alone —
  e.g. a different forge (cross-forge within one stack).

> Governance governs exactly what it declares, section by section. Silence = local authorship.

**Distribution:** per entry, governance derives the write forge + token (from the resolved `repos`
preset + the location's org) and paths, writes the satellite's managed config = identity (branded entries
only) + **preset references pointing back at `governance.source` as tracked refs** (so preset content
changes propagate on the next run **without re-reconcile**), and **seeds the cache** as fallback.
Re-reconcile only to change *which* presets a repo uses, its identity, or its pins.

The spectrum, per repo and per section: **pure-native** (all local) → **governed-with-local-tweaks**
(identity central, a few functional overrides local) → **pure-governed** (thin pointer; even deviations
authored centrally in the entry's `config:`). The repo's actual code is untouched by any of it.

## 9. Fact-resolution architecture

Today three surfaces each keep a private placeholder list with no shared dispatch: cistate bodies
(`src/cistate/facts.go`), badge SVG values (**two near-duplicate generators** —
`src/postbuild/badges.go` + `src/cli/cmd/badges.go`), and the gitver leaf-pass
(`src/gitver/template.go`).

> **All facts resolve through one provider registry.** Providers declare dependencies and resolve in
> topological order (§4.1); each surface consults the same registry; the two badge generators are one.

This is prerequisite work (a pure refactor registering the existing families unchanged first), so the
new families (`{project.module}`, `{path.*}`, `{org.*}`, `{metadata.*}`, `{slug}`) are one-place adds
and the ordering rule has a single home. The registry is injected as a resolver func so `gitver` need not
import `config`.

## 10. Candidate invariants (graduate to `invariants.md` when enforced)

1. **Location is the single identity fact.** `org`/`slug`/`{path.*}` derive from the anchor; a config
   cannot both declare a conflicting `org`/`slug` and an `at:`. *(Enforce: derivation is the only source;
   an explicit `org:` is an override, validated against `orgs`.)*
2. **Identity ≠ credentials (keep hard).** Identity/path resolution is declarative data
   (`org → alias → path`); credential resolution is a secret lookup (`primary-forge + org → token NAME`,
   from CI vars). The token name is **computed, never declared**; `orgs` stay credential-free; a
   credential is not an identity alias.
3. **Forge is a preset concern, never an org property.** The write forge comes from the resolved `repos`
   preset (overridable per entry), so a stack is not pinned to one forge and an org is not pinned to one
   forge.
4. **A named surface value is singular.** Only a scoped *default* may be a tiered `StringOrList`; a list
   under a named surface is a hard error.
5. **Surface ids are disjoint.** Forge ids and registry ids share one namespace so `{path.<id>}` is
   unambiguous.
6. **Presets resolve from source; the cache is never mandatory for a tracked ref.** Resolution (dry-run
   or real) must succeed from source with an absent cache, and must fail *clearly* when a tracked ref is
   unreachable and uncached.
7. **Governance governs only what it declares (presence-gated, per section/entry).** A location-only entry
   never suppresses a repo's local `metadata`; a branded entry never silently merges with one.

These compose with existing invariant #1 (one construction path, `loadResolved`) and #2 (presets resolve
before decode) — the source-fetch loader is inside that one path, not a bypass.

## 11. Schema (Go, indicative)

```go
type OrgConfig struct {
    Maintainer string
    Aliases    map[string]string // open; e.g. {handle: hlhd, gitlab_group: "PrPlanIT/HomeLabHD"}
}

type MetadataConfig struct {
    Org         string             // ref into orgs (usually derived from the anchor)
    Title       string             // human display name; default = slug
    Names       map[string]string  // per-surface repo-path override; default = slug
    Description  Scoped[StringOrList] // default may be tiered; named overrides are scalar
    Readme       Scoped[string]      // default or per-surface paths; never tiered
    Topics      []string
    License     string
    Category    string
    Website     string             // a *separate* site only, never the repo URL ({path.<forge>})
    DocsURL     string
    Icon        string
    Labels      map[string]string  // open OCI-annotation-style escape hatch; NOT a back door for typed fields
}

// Scoped[T]: plain form = the default T; map form = {default: T, <surface>: …}.
// Ordered id-maps (orgs, stacks, catalog repos) decode via decodeIDMap[T] (src/config/identity_map.go),
// modeled on OrderedClusters (src/config/gitops.go). All decode under KnownFields(true).
```

## 12. Deferred / open (completeness audit)

Resolved in-model: language-aware `{project.module}` (C1), open `labels` map (C2), per-surface scoping
(C6). **Decide when the need appears** (do not pre-build): lifecycle/status incl. `visibility` (C3 —
needed first if governance *creates* repos), repo-level `authors` (C4), structural About URLs (C5, else
`labels`), richer registry auth shape / `api_url` (C7), per-surface org, multi-stack layering, a paused-
repo flag (C7). `labels` (C2) makes most of these addable later with no schema bump.

## 13. Build order

**Deliverable 0** (cache path, done → reframed as fallback) → **shared fact resolver** (§9) →
**identity primitives + facts** (§2–4, usable natively; exit criterion: convert real StageFreight +
hasteward builds to one shared `build-*-binary` preset and confirm correct per-repo module paths) →
**source-aware preset loader** (§7) → **governance refactor + catalog** (§8) → **rewire consumers**
(publish metadata, license badge). See the working plan for file:line touchpoints.
