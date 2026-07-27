# Cross-Forge Distribution — the model

> Repo-only architecture spec. How **git refs, release entries, and release assets**
> move from the primary forge to mirrors. Authoritative for `publish` release channels,
> `repos.*.sync`, `release create`, and `release sync`. Written because this was being
> re-derived ad hoc; it should be derived once, here.

## 1. Actors

- **Forge** — a hosting provider (`gitlab`, `github`, …) under `forges:`, with a base URL + credentials.
- **Repo** — a forge project under `repos:`, with `roles:`. Roles span **two orthogonal axes**:
  - **Authority** — `primary` (the **source of truth**; releases are *authored* here) vs `mirror` (*shadows* the primary; never the source).
  - **Public availability** — `publish-origin`: **the forge external, unauthenticated users actually reach.** This is a **contract**, not a badge convenience: *anything that must resolve for the public* — release **binaries**, badge SVGs, raw README/media, download links — **must exist here, or those requests 404.** In SF, `primary` is `gitlab.prplanit.com` (internal, **not publicly reachable**) and `github-mirror` is `publish-origin` (public). The axes are independent: a publicly-reachable primary could itself be `publish-origin`.
- **Registry** (`registries:`) and **pages provider** are *other* destination types — out of scope here; this doc is refs + releases only.

## 2. Distribution is THREE independent planes

The single biggest clarity win: cross-forge replication is not one thing. Three planes move **separately**, each with its own mechanism, trigger, and coverage. Every historical bug here comes from conflating them.

| Plane | What moves | Primitive |
|---|---|---|
| **P1 — Git refs** | branches, tags (the commit graph) | git push mirror |
| **P2 — Release entries** | tag + name + notes + prerelease flag (the release *object*) | forge `CreateRelease` |
| **P3 — Release assets** | **binary files** — re-hosted on the mirror. (Registry links ride *inside* P2's portable body, not as separate assets — see §4.) | forge `UploadAsset` |

A release on the primary is P2 **+** P3 together. A faithful mirror must carry **all three** planes — and today it doesn't (see §5).

### The publish-origin contract — why P3 is non-negotiable

The planes aren't equally optional, and the reason is `publish-origin` (§1). The **primary is internal** — GitLab (`gitlab.prplanit.com`) does not serve external, unauthenticated users. The **publish-origin (the public mirror) is the only forge the outside world can reach.** So the contract is:

> **Every publicly-referenced artifact must resolve on `publish-origin`, or it 404s for the public.**

That is the whole justification for cross-forge replication — it is not aesthetics or convenience:

- A **badge** SVG is served from `raw.githubusercontent.com/.../scribe/*.svg` **because** GitHub is publish-origin; the GitLab raw URL would 404 externally.
- A **release binary** download link handed to a user must point at publish-origin — GitLab's asset URL is unreachable for them.
- Scribe's public producers (`go-report`, `last-commit`, `issues-open`, …) resolve their repo from `roles: publish-origin` for the same reason.

This is exactly why a **notes-only** release mirror is a broken contract, not a partial success: the release *entry* appears on GitHub (P2 ✅) but every asset link points back at the unreachable internal primary (P3 ❌) — so the public sees a release they **cannot download**. Honoring publish-origin means P3 must cross with P2, always. That reframes §5's G1 from "a nice-to-have" to "the mechanism that makes the publish-origin contract true for releases."

## 3. Mechanisms (current implementation)

| Mechanism | Code | Trigger | P1 refs | P2 entries | P3 assets | Scope |
|---|---|---|---|---|---|---|
| **Git mirror** | `src/sync/git_mirror.go` | push/CI | ✅ `sync.{branches,tags}` | — | — | reconciling (add+prune per scope) |
| **Channel-authored** | `release_create.go` → `projectAuthoredRelease` | release event | — | ✅ | ✅ **binaries** | forward-only (the release being made now) |
| **Sync-projected** | `release_create.go` → `projectToMirror` | release event | — | ✅ | ❌ **notes only** | forward-only |
| **`release sync`** | `release_sync.go` → `runReleaseSync` | manual/scheduled | — | ✅ (missing only) | ❌ **notes only, no prune** | reconciling entries, **assets absent** |

Two destination surfaces feed P2/P3:
- **Channel `publish.<ch>.repos:`** — "author *this channel's* releases to these forges." Carries assets. Forward-only.
- **Repo `repos.<m>.sync.releases`** — "passively replicate the primary's releases here." Notes-only today.

## 4. What actually crosses — the generator already made it portable

Inspecting a live GitLab release settles this: **there is no P3 "translation" and no embed subsystem.** The generated release **body** is already **forge-agnostic** — an Image-Availability table linking the *registries* (Docker Hub, Harbor, GHCR), digest `docker pull …@sha256:…` commands, a Downloads table by *filename + size + SHA-256*, checksums, highlights. **Not one GitLab-specific URL.** It renders on GitHub byte-identically and correctly, because the generator is registry-centric, not forge-centric.

The only forge-specific, externally-unreachable thing is in `assets.links`: the **binary tarballs**, hosted as GitLab **upload URLs** (`gitlab.prplanit.com/-/project/37/uploads/…`).

So crossing GitLab→GitHub is exactly **two moves**:

- **Body → copy verbatim.** It's portable already, and verbatim *is* consistency — identical notes on both forges.
- **Binaries → re-host.** Fetch each tarball from the GitLab upload (CI can reach it) and **upload it as a real GitHub release asset**. The body's Downloads table then names actual downloadable files. A **file re-host**, not a link-morph.

Registry links need **nothing** — they're already markdown in the portable body, pointing straight at the registries. No link-asset plumbing, no public-filter, no exposure model, no morphing. The subsystem those would have required does not get built.

*(One cosmetic wrinkle: the internal `cr.pcfae.com`/Harbor row is a markdown link an external clicker can't reach. **Kept** for verbatim consistency — it's labeled internal, with Docker Hub/GHCR public rows beside it. Stripping it would require per-forge bodies — the forge-specific complexity we're deliberately avoiding.)*

## 5. The gaps (current → target)

- **G1 — `release sync` is notes-only and add-only.** It backfills release *entries* but drops **all assets** (`release_sync.go:140` calls `CreateRelease` with no `UploadAsset`) and never prunes (finds `missing`, ignores extras even under `scope: exact`). → Binaries published on GitLab are **not downloadable** on GitHub. **This is the priority fix:** `release sync` copies the body **verbatim**, **re-hosts the binary assets** (fetch from primary → `UploadAsset` on the mirror), and prunes to match under `exact`. No embed/translation layer — the body is already portable (§4).
- **G2 — retention follows only P2-sync mirrors.** `retForges` (`release_create.go:854`) = primary + `sync.releases` mirrors; a channel-`repos:` mirror is authored-to but never pruned → accumulation. → Retention must follow the **union**: primary ∪ channel `repos:` ∪ `sync.releases` mirrors.
- **G3 — the XOR is a comment, not a rule.** "A repo receives releases via a channel `repos:` **xor** `sync.releases`, never both" is enforced by prose. → Validation must reject both-at-once (double-delivery).
- **G4 — SF's dev channel is GitLab-only.** `dev-release.repos: [primary]`. → `[primary, github-mirror]`, `keep_last: 3`.

## 6. Target model — two orthogonal capabilities

Cross-forge release distribution is **two independent, composable capabilities**, not one mechanism with modes. One is expressive; the other is dumb and deterministic.

### Axis 1 — Author (expressive: "release anywhere, no backstory")

`publish.<ch>.repos:` authors a release to **any** forge/repo listed. It is standalone — it implies no mirror relationship and reflects nothing. A one-off release on a single forge is just a channel naming that repo. Retention is the channel's own policy, applied per destination. This axis stays fully open — releases with or without any sync.

### Axis 2 — Sync (predictable: a 3-value, content-aware reconciler)

`repos.<m>.sync.releases: <mode>` reflects the **primary** onto a mirror. No cleverness — three values:

| Mode | Behavior |
|---|---|
| **off** (omit) | nothing replicates |
| **additive** (`all`) | create missing **+ update changed** — never deletes |
| **exact** (`all` + `prune`) | additive **+ prune** → mirror equals primary 1:1 |

Sync is **content-aware, not presence-only**: it compares the **asset set / digests / notes**, not just "does the tag exist," so a re-signed asset or edited notes on primary **refresh** on the mirror (that's what makes `additive` more than a one-shot copy and `exact` a true reflection). `prune` (delete) happens **only** under `exact`.

#### Git-ref facets (`branches` / `tags`) — divergence + scope

The `branches`/`tags` facets take the same mode scalar, plus two P1-specific refinements. Scalar sugar stays the common path (`branches: exact`); reach for the map form only when you need them.

- **Keep-divergent is the default, not a knob.** If a ref on the mirror has *diverged* from primary (an independent commit, a community push), sync **does not force over it** — it skips the ref and surfaces the drift. Force-clobbering a diverged ref is the ref-plane version of deleting a contributor's branch, so provenance-scoping forbids it by default (fail-safe, like the release cliff-guard). The aggressive behavior is an explicit opt-in:
  ```yaml
  branches: { mode: exact, force: true }   # overwrite even diverged refs — rare, deliberate
  ```
  With `force` absent, a diverged ref is logged, never overwritten. (Applies to the update path in both modes — `additive` updates a changed ref too, and won't clobber a diverged one either.)

- **`only:` scopes which refs mirror.** Default `all`; restrict to the forge's protected branches or explicit patterns, so **internal WIP branches never leak to a public mirror**:
  ```yaml
  branches: { mode: exact, only: protected }            # forge's protected-branch list (forge-aware)
  branches: { mode: exact, only: [main, "release/*"] }  # explicit patterns (forge-agnostic)
  ```

Both lean toward safety — you never clobber divergence, and you opt *into* wider exposure rather than out of it — and both stay off the common path: `branches: exact` alone is unchanged.

### Composition — the one rule

The axes mix, with a single predictable interaction:

- **off / additive + authoring** → compose freely. `additive` never deletes, so a release authored *only* on the mirror survives alongside reflected ones ("manual releases with no backstory").
- **exact + independently authoring to the same repo** → conflict *by design*: `exact` declares the repo a strict 1:1 reflection of primary, so a mirror-only release **will** be pruned. Rule: **`exact` = reflection; do not co-author there.** Validation warns. `additive`/`off` carry no such restriction.

So: **prune only ever happens under `exact`; every other combination is add/update-only and safe to compose.**

### Invariants across both axes (the contract)

- **Body + binaries travel together, always.** No mechanism delivers a release entry (verbatim body) without **re-hosting its binaries** on the mirror. A notes-only mirror is a *broken publish-origin contract* (§2) — the entry shows but the download 404s.
- **Fail-closed** — never prune or delete-on-drift when the source can't be read.
- **Content-aware update** — refresh the mirror when the body text or an asset digest drifts (a re-signed binary), so the copies stay consistent over time.

### Retention placement

Retention (`keep_last`) is a **primary** concern — authored channels prune on the primary. An `exact` mirror **inherits** primary's retained set via prune (no separate policy). An `additive` mirror keeps whatever it's given (the operator chose not to prune). A non-primary repo you *author* to (Axis 1) prunes per that channel's `retention`.

### The reader's test
> *"Does GitHub have the same releases as GitLab — same notes, downloadable binaries — kept consistent?"*
> `sync.releases: exact` on `github-mirror` — one line. It converges to yes by invariant: the mirror is *defined* as a reflection, the body copies verbatim, the binaries re-host, and content-aware update keeps them aligned. To additionally publish a forge-specific one-off, an Author channel names that forge — orthogonally.

## 7. Applying the model to SF

**Primary (GitLab) is authored; `github-mirror` is a reflection.** SF authors all releases on the primary via channels (`primary-release`, `dev-release`) with retention there; GitHub receives them by **reflection**, not authoring:

```yaml
github-mirror:
  roles: [mirror, publish-origin]
  sync: { branches: exact, tags: exact, releases: exact }   # reflect ALL three planes
```

- **Dev (`keep_last: 3` on primary)** — GitHub's `exact` sync inherits primary's current 3 + `latest-dev`: the verbatim body + binaries re-hosted as GitHub assets. Backfill is moot — GitLab already prunes history.
- **Stable** — authored on primary (permanent); GitHub reflects each via `exact`, with the verbatim body and re-hosted binaries. Self-healing: a missed pipeline or manual release converges on the next sync.
- **The `github-release` channel and any `dev-release` GitHub `repos:` are removed** — GitHub gets everything through sync, one declaration, no per-channel duplication.
- **Historical stable (≤ v0.7.0)** — `exact` reflection will attach binaries + prune to match on the first post-G1 sync; nothing to hand-backfill.

*(Axis 1 stays available if SF ever wants a forge-specific one-off — e.g. a release only on a third forge — without making it a mirror. Not needed today.)*

## 8. Work items

1. **G1 — the `release sync` reconciler upgrade (§9).** Verbatim body copy, binaries re-hosted from primary, content-aware add/update, `exact` prune, fail-closed. This is the load-bearing piece — it's what makes `sync.releases` honor the publish-origin contract, and everything else depends on it.
2. **G2 — retention placement.** Authored channels prune on primary; an `exact` mirror inherits via prune (no separate policy). The only per-mirror retention is for a non-primary repo you *author* to (Axis 1) — that already follows the channel's `repos:` once G1's retention wiring lands.
3. **G3 — `exact` + co-author validation.** Warn when a repo is both an `exact` sync target and independently named in a release channel's `repos:` (its mirror-only releases would be pruned). `additive`/`off` need no check.
4. **G4 — SF config.** `github-mirror.sync.releases: exact`; delete the `github-release` channel; `dev-release` stays `repos: [primary]`, `keep_last: 3` on primary.

Order: **G1 first** (it's the enabler; without it `sync.releases` is a broken contract), then G4 flips SF to the reflection model, then G2/G3 tidy retention + validation.

## 9. Scope — the `release sync` reconciler upgrade (G1)

Today `runReleaseSync` (`src/cli/cmd/release_sync.go`) is presence-only: for each primary release missing on a mirror it calls `CreateRelease` with notes, no assets, no update, no prune (`:112-156`). The upgrade turns it into a **content-aware, mode-driven reconciler** that honors the publish-origin contract.

### 9.1 Forge interface additions (`src/forge`)

`forge.Forge` today has `ListReleases`, `CreateRelease`, `UploadAsset`, `DeleteRelease`. Add:

- `ListReleaseAssets(ctx, releaseID) ([]ReleaseAsset, error)` — the **file** assets (tarballs), with `Name` + `Size`/`Digest` where the forge exposes it. (GitLab hosts binaries as `assets.links` type `other`; the archive filenames — known from the `archives:` recipe — identify which links are our files vs. registry refs to leave in the body.)
- `DownloadAsset(ctx, asset) (io.ReadCloser, error)` — stream a file asset from the source forge.
- `DeleteAsset(ctx, releaseID, assetID)` — for update-in-place (replace a drifted asset).
- `UpdateReleaseNotes(ctx, releaseID, body)` — for body drift (verbatim re-copy).

GitLab hosts binaries as upload links; GitHub as uploaded assets (§4). The interface hides that one difference: **read a file asset from primary, upload it to the mirror.** Registry refs stay untouched inside the portable body — no link handling.

### 9.2 The reconcile algorithm (per mirror with `sync.releases: <mode>`)

```
primary_set  = primaryForge.ListReleases()                 # source of truth
if primary_set unreadable: ABORT this mirror (fail-closed)  # never prune on partial info
mirror_set   = mirrorForge.ListReleases()

for each pr in primary_set:                                 # ADD + UPDATE
    mr = mirror_set[pr.tag]
    if mr is nil:            create entry + attach assets(pr)         # ADD
    else:                    reconcileAssets(pr, mr); reconcileNotes(pr, mr)   # UPDATE-on-drift

if mode == exact:                                          # PRUNE (exact only)
    for each mr in mirror_set where mr.tag not in primary_set:
        mirrorForge.DeleteRelease(mr)
# mode == additive: skip prune entirely (mirror-only releases survive)
```

- `reconcileAssets` diffs by **name + digest/size**; re-uploads changed file assets (the "signature updated" case), leaves unchanged ones untouched. Idempotent — a second run is a no-op.
- `reconcileNotes` compares primary's body to the mirror's; on drift, re-copies it **verbatim**. Under the managed-block boundary, any human prose *outside* SF's region survives (additive curation); a pure `exact` reflection has no such region — the body is entirely SF's.

### 9.3 Asset acquisition (P3 = copy from primary)

File assets are **streamed from primary → uploaded to the mirror** (primary owns the bytes). Runs where both forges are reachable — CI is internal, so it reads GitLab and writes GitHub with `GITHUB_TOKEN`. No rebuild: `release sync` reconciles arbitrary/historical releases, so it must copy, not re-produce. (Private-key material is refused, mirroring `projectAuthoredRelease`.)

### 9.4 Modes (from §6)

- **additive** (`scope: all`) — run ADD + UPDATE, skip PRUNE.
- **exact** (`scope: all, prune: true`) — ADD + UPDATE + PRUNE.
- **off** (omit) — mirror not visited.

`--dry-run` prints the full ADD/UPDATE/PRUNE plan (body drift, per-asset re-host/replace, prune) and mutates nothing.

### 9.5 Invariants (enforced, not assumed)

- **P2+P3 atomicity:** a created/updated mirror release that fails asset attach is a **loud failure**, not a silent notes-only release.
- **Fail-closed:** primary unreadable → abort that mirror, prune nothing.
- **Idempotent:** in-sync input → zero mutations, zero output churn.

### 9.6 Verification

- Unit: reconcile diff (add / update-on-digest-change / prune-under-exact / no-op-when-synced) against fake forges; verbatim body copy; body-drift re-copy; managed-block prose preserved.
- Integration: GitLab (fake) with a body + file assets → GitHub (fake); assert **body copied byte-identical**, **binaries uploaded as real assets** (downloadable), registry links present in the copied body untouched, `exact`-prune removes a mirror-only tag, `additive` keeps it, **foreign tag never touched**.
- Dry-run parity: `--dry-run` plan equals what apply performs.

### 9.7 Touch points

- `src/forge/*.go` — interface + gitlab/github impls of `ListReleaseAssets` / `DownloadAsset` / `DeleteAsset` / `UpdateReleaseNotes`.
- `src/cli/cmd/release_sync.go` — `runReleaseSync` → reconciler (mode-driven; today's body is the `additive`-without-update subset).
- `src/config/sync.go` — `FacetSpec` already models additive-vs-exact (`scope`/`prune`); wire the mode + the archive-filename recognition (which `assets.links` are *our files* to re-host vs. registry refs to leave in the body — known from the `archives:` recipe).
