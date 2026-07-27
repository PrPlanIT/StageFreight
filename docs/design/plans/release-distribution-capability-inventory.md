# Release Distribution — Capability Inventory

> Repo-only. The **complete** operator-intent landscape for cross-forge **release +
> mirror + retention**. This is a *living, additive* map: nothing here gets cut —
> `deferred`/`gap` items are *homes to design toward*, not things to shave. Status
> tags (`settled` · `open` · `deferred` · `gap` · `principle`) are orientation, not
> judgment. Adjacent publish channels (images, packages, pages, metadata, announce)
> live in the sibling [Distribution Surfaces map](distribution-surfaces-map.md).
>
> Design rule this exists to serve: **completeness of capability, progressive
> disclosure of expression** — clarity comes from defaults + layering, never from
> removing options.

## 1 · Authoring (make / publish a release)
- Publish a release to a forge — `settled`
- Publish to **multiple** forges independently (genuine co-author, not mirroring) — `settled`
- Ad-hoc one-off to a specific forge, no config edit (CLI override) — `open`
- Upsert / re-publish to fix an existing release (content-aware, replace changed assets) — `settled`
- Release types: stable / prerelease — `settled`
- Rolling aliases (`latest`, `latest-dev`, `v{version}`) — `settled`
- Tag templates (`dev-{sha:8}`, `v{version}`) — `settled`
- Attach binaries (archives) + checksums — `settled`
- Rich notes generation (security summary, image-availability, downloads table, highlights) — `settled`
- Draft releases — `open`
- Signing (cosign / profiles) attached to the release — `settled`

## 2 · Retention (manage the set over time)
- `keep_last N`, restic buckets (`keep_daily/weekly/monthly/yearly`) — `settled` (shipped)
- Per-series grouping by `(template, identity)` — `settled` (shipped)
- `keep_branches N` (bound identity groups) — `settled` (shipped)
- Rolling auto-exempt (`latest-*` never a prune candidate) — `settled`
- `protect` patterns (explicit never-delete override) — `settled`
- `identity:` override (custom partition dimensions) — `settled`
- `0 / -1 / unset = ∞` semantics — `settled`
- Provenance-scoped prune (only prune what SF created; foreign untouchable) — `settled`
- **Independent mirror-side retention** ("keep 3 on primary, 10 on the public archive") — `gap`
- Branch-aware GC / drop-retired (dead-branch tag cleanup) — `deferred` (#45)
- Registry (image) tag retention — shared per-series engine, also serves the sibling map — `settled` (shipped)

## 3 · Mirroring (reflect primary → mirror, cross-forge, **we own it — forge-agnostic**)
- Per-plane on/off: `branches` / `tags` / `releases` — `open`
- Branch scope: `all | protected | [patterns]` — `open`
- **Tag scope / filter** (don't mirror internal/nightly tags to public) — `gap`
- **Release scope / exclusion** ("internal-only, never publish") — `gap`
- `prune` (mirror deletions → 1:1, vs additive) — `open`
- `force` / keep-divergent (clobber vs preserve diverged refs) — `open`
- Per-facet override (each facet its own prune/force/scope) — `open`
- Content-aware update (digest diff; re-sign propagates) — `settled`
- Verbatim body copy — `settled`
- Binary re-host (fetch from primary → upload to mirror) — `settled`
- Self-healing reconciliation (missed release converges) — `settled`
- **Mirror cadence** (per-push / cron / `serve` / on-demand) — `gap`
- Pull / bidirectional mirroring (fork tracks upstream) — `deferred`
- Git LFS object mirroring — `gap/niche`

## 4 · Public-facing contract (`publish-origin`)
- `publish-origin` role — the forge the outside world reaches — `settled`
- Internal-vs-public forge distinction (declared exposure) — `open`
- **Strip internal refs/links from the public surface** (Harbor-link 404 case) — `gap`
- Public-embed filtering (only publicly-reachable links cross) — `deferred`
- Forge-relative link morphing (per-forge equivalents) — `deferred`
- Badge / raw-content resolves on publish-origin — `settled`

## 5 · Notes & curation
- Managed-block (SF owns a marked region; human prose preserved) — `open`
- **Surgical curation** (refresh SF's block *and* keep custom prose — vs leave-alone) — `open/ambiguous`
- Per-forge independent notes (rich GitHub vs terse GitLab) — `deferred`

## 6 · Governance & lifecycle
- Retract / remove a release across forges — `settled`
- **Internal-only exclusion** ("keep on primary, never publish") — `gap` (homes as a release-facet scope, §3)
- Redaction (leaked-secret removal, deliberate) — `settled` (= retract)
- Embargo / scheduled-coordinated release — `deferred`
- Restore a pruned release (archive / cold tier) — `deferred`
- Promotion / staged rollout (lanes) — `deferred`

## 7 · Topology & roles
- Authority: `primary` xor `mirror` — `settled`
- Availability: `publish-origin` (orthogonal, on either) — `settled`
- `foreign` (referenced by a run, no role) — `settled`
- Multiple mirrors (fan-out to N) — `settled`
- Forge-agnostic (gitlab / github / gitea / bitbucket / …) — `settled`

## 8 · Cross-cutting (must hold across all of the above)
- **Safe defaults** (never delete / never clobber until opted in) — `principle`
- **Fail-closed + loud** (unreadable source → do nothing + alert) — `settled`
- **Cliff-guard** (mass-prune reads as a bug → abort) — `settled`
- **Freshness signal** (stale mirror screams) — `gap`
- **Progressive disclosure** (shorthand glance ↔ longhand scalpel, no capability lost) — `principle`
- **Zero-cost when unused** (single-forge users never see mirror/roles) — `principle`
- **Content-aware, not knob-driven** (upsert diffs by digest — no append/replace/keep-existing mode) — `principle`

---

## Open design work — homing the gaps (additively, reusing existing mechanisms)
- **Internal-only exclusion / tag scope** → a scope on the `releases`/`tags` facet (same as `branches: only:`), `only:`/`exclude:` by pattern.
- **Surgical curation** → decide `force: false` semantics (leave-alone vs update-my-region), then home it.
- **Mirror cadence** → where "on push / cron / on-demand" lives.
- **Independent mirror retention** → does it reuse `retention:` on the facet?
- **Strip-internal-from-public** → opt-in body shaping.
- **Freshness signal** → observability, not config.
