# Distribution Surfaces — Map

> Repo-only. Everything StageFreight **publishes/distributes beyond the forge release
> object** — sibling to the [Release Distribution Capability Inventory](release-distribution-capability-inventory.md),
> kept separate so neither map gets muddy. Same conventions: living, additive, status
> tags are orientation not judgment; nothing here gets cut.

## Container images → registries
- Multi-registry publish (dockerhub / ghcr / harbor) — `settled`
- Content-addressed blobs (interchangeable copies, no "origin") — `settled`
- CRI-O transparent pull-through routing — `settled` (cluster infra)
- Registry exposure (public / internal) — feeds any future embed policy — `deferred`
- Retention = the shared per-series engine (see core inventory §2) — `settled`

## Binary artifacts → package registries
- Generic-package publishing (forge generic registry; rolling `latest-dev` + immutable `dev-{sha}`) — `settled`
- OS packages: deb / rpm / apk (nfpm-style — a library to adopt à la carte, not GoReleaser-coupled) — `deferred`
- Package-manager taps: Homebrew / Scoop / AUR / nix — `deferred`

## Project identity → registries + forges
- Metadata sync: description · topics · website · readme — `settled`
- Typed dual-destination (registry Overview/Info **and** forge About panel) — `settled`
- Length-tiered description variants per destination cap — `settled`

## Static site → pages
- Pages deploy (Cloudflare / GitHub Pages) — `settled`
- Build-tree → hosted site + domain wiring — `settled`

## Notification → channels
- Announce on release (Slack / Discord / Mastodon / ntfy / email) — `gap`

---

**Relationship to the core inventory:** these surfaces share primitives with core
(the retention engine, the `publish:` channel model, the forge/registry topology) but
are *distinct distribution targets*, not the release-object-across-forges. Design them
against the same principles — completeness of capability, progressive disclosure,
safe defaults — but on their own timeline.
