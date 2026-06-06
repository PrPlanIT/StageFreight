# Release-Channel Ref-Minting Contract — Names over a Single Identity Anchor

> **Status: living design document.** The backend contract for the **naming layer** of
> [[release-channels]] — the twin of [[release-channel-materialization-contract]] (projection layer).
> Small by construction: once the single-anchor invariant holds, ref-minting carries almost no
> semantic burden. Iterate it *here*.

> **Implementation is gated.** Shares the materialization contract's prerequisite (a store-resident
> identity to name). This document existing is not a trigger to build.

## What a ref is anchored to (the whole question, already answered)

A forge release projection needs a ref to hang on. When the trigger that produces an identity is
itself a ref (a pushed `rc-{version}` tag), the anchor exists. When it is not — a branch push, a
schedule — the backend **mints** a backing ref (e.g. `dev-{sha:8}`).

The only question ref-minting could raise is *"what is the ref anchored to?"* — and the
single-anchor invariant ([[release-channels]] Invariant 6) already answers it:

> A ref is a **naming pointer to a certified set** — the release identity ([[release-channels]]
> Invariant 6). It names **an identity, never "a release" and never "a store entry."**

That collapses the dual-graph risk (`ref → release` vs `ref → certified set`) before it can appear.
A ref is an index entry in the naming layer; the identity it names is the certified set, held by no
storage system in particular.

## The rule (one sentence)

> A minted ref binds a name to a certified set (the identity); it carries no bytes, no trust, and no
> identity of its own.

## Conformance (each a test target — most are the single anchor, restated at the naming layer)

- **R1 — Names, never anchors identity.** Resolving a ref yields the certified set (a digest set),
  never a forge-, tag-, store-, or manifest-resident object. (Invariant 6 at the naming layer.)
- **R2 — Carrier, not identity.** Deleting or recreating a ref (on prune, or alias repoint) changes
  *distribution*, never *identity*. The certified set is unaffected by what happens to the ref.
- **R3 — Binds to an exact certified set.** The ref→identity binding names one certification,
  identified by the identity digest (the equality primitive, [[release-channels]] watch-list). The
  binding is reproducible: resolve the same ref, get the same certified set.
- **R4 — Idempotent.** The same trigger for the same identity mints the same ref binding to the same
  certified set — never a divergent one. (`verify` equality holds across re-runs.)
- **R5 — No trust surface.** Verification never consults a ref; it resolves ref → certified set and
  verifies *there*. A ref cannot be a `verify` input. (Same family as
  [[persistence-identity]]: a handle locates, it does not bless.)
- **R6 — Not release lineage.** A channel ref must never be read as a human release tag. **Traced:**
  the `tag_sources` search path and changelog boundary are already safe (pattern-scoped to semver;
  a non-semver `dev-{sha:8}` matches neither). The actual exposure is the *unfiltered*
  `gitstate.ExactTagAtHEAD` scan (`headAtTag`, `ci/context.go` local fallback). Under the single
  anchor this is no longer a semantic edge case — it is one rule: **don't confuse the naming index
  with the identity space.** Guard: `ExactTagAtHEAD` and its callers must ignore channel refs
  (restrict to `tag_sources`-matching refs, or exclude the channel namespace).

## Naming discipline (inherited, not re-decided)

`dev-{sha:8}` — hyphen, mirrors docker, user-defined pattern. The `+` build-metadata stays in the
archive *filename*, never the ref ([[release-channels]] Locked decisions). A ref name may *encode*
provenance (a sha) for readability, but it *resolves* to a certified set, never to the commit — R1.

## Prerequisite (shared)

Ref-minting has nothing to bind to until the certified output set is store-resident — the same
blob-capable-persistence prerequisite as [[release-channel-materialization-contract]]
(`PersistenceKind` has no blob variant; `cas` is OCI-layout-only). R3/R4 also presume the identity
digest (the deferred serialization spec). No new prerequisite of its own.

## Acceptance

Satisfied when: resolving any minted ref yields the certified set it names, idempotently
(R3/R4); `verify` consults only that identity, never the ref (R5); and `ExactTagAtHEAD`-class scans
exclude channel refs (R6). At that point both backend contracts are closed and the `Commit 1…N`
staging plan in [[release-channels]] derives mechanically.
