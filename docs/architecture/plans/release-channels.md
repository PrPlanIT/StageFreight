# StageFreight Release Channels — Aliases, Identities & Retention

> **Status: living design document.** The durable home for StageFreight's release-channel model
> (rolling prerelease/dev binary distribution and its symmetry with registry tag retention).
> Iterate it *here*.

> **Conceptual model: settled.** Truth is a **verification relation** — `certification ⋈ resolved
> bytes ⋈ digest-equality` — and **no single store owns it** (Invariant 10). Three concerns:
> *provenance* (Git), *certification* (the signed certified set — the *identifier* names resolve to;
> Invariant 6), *availability* (the bytes, in distribution backends — Model C, StageFreight stateless).
> "Release identity" in this doc = the certification record, *not* a truth surface; Git is
> root-of-claim, not root-of-artifact. No required durable byte store (the "blob-capable content store
> prerequisite" is **retracted**). Both backend contracts drafted (gated):
> [[release-channel-materialization-contract]] (projection layer) and
> [[release-channel-ref-minting-contract]] (naming layer). **Real prerequisite (much smaller):** a
> Git-resident *signed* identity certificate (`outputs.json` → birth-certificate + signing, in
> flight). After that, the `Commit 1…N` staging derives mechanically.

> **Implementation is gated** pending explicit design sign-off. This document existing is not a
> trigger to start editing `release_create.go` / `retention` / the forge backends.

## Context

The capability question: *how do prerelease/dev **binaries** get a rolling, self-pruning
distribution channel — "always the newest, plus the last few exact builds" — without
accumulating protected tags?* The registry side already has this: `dockerhub-dev` publishes
`dev-{sha:8}` + `latest-dev` with `retention: { keep_last: 5, protect: [latest-dev] }`. The goal
is the **same capability for forge releases**, expressed in the same vocabulary, so a consumer's
mental model is identical across artifact kinds:

```
docker pull prplanit/stagefreight:latest-dev      curl …/latest-dev/stagefreight-linux-amd64.tar.gz
docker pull prplanit/stagefreight:dev-0420ec8      curl …/dev-0420ec8/…-0.6.1-dev+0420ec8-linux-amd64.tar.gz
```

This is **not** a binary-release feature, and it is **not** a "dev releases on every push"
feature. It is a cross-artifact *channel* capability: it defines what happens **once an immutable
identity exists** — nothing about what creates one. Binaries are merely the first consumer that
forced it into the open (the v0.6.1 archives-didn't-attach incident, see
[[stagefreight_perform_publish_artifact_boundary]], surfaced the binary release path at all). The
trigger that mints an identity — push, tag, merge, nightly schedule, post-review — is ordinary
StageFreight `when:` scheduling and lives entirely outside this model.

## Locked decisions

- **Immutable identifier mirrors docker, hyphen not plus.** Release identity `dev-{sha:8}`,
  rolling alias `latest-dev` — byte-for-byte the same vocabulary as the container tags, so the
  registry↔release correspondence is 1:1. `+` is a legal git ref char but is hostile downstream
  (URL-encodes to `%2B`; forge asset-download URLs, shells, and some forge UIs treat it
  inconsistently). The `+` stays where it is genuinely semver build-metadata: the **version string
  inside the archive filename** (`stagefreight-0.6.1-dev+0420ec8-linux-amd64.tar.gz`), which
  carries full provenance. Clean identifier on the ref, full lineage in the file.
- **Names are user-defined; StageFreight hardcodes none.** The immutable pattern (`dev-{sha:8}`)
  and the alias names (`latest-dev` / `nightly` / `preview` / `canary`) are entirely the user's.
  The moment StageFreight cares about a specific name, the abstraction is gone.
- **Retention protects by name, not by specialness.** Exactly the registry semantics:
  `retention.protect: [latest-dev]`. An alias survives because the user named it protected, not
  because it is a distinguished kind of object.
- **Prune removes the whole artifact.** A pruned identity takes its forge release *and* its git
  tag with it (today `forgeStore.Delete` only `DeleteRelease`s — leaves the tag; see Gaps).
- **The identity is the certified output set.** Not the commit (provenance), not a single digest
  (there are many) — the manifest-defined collection certified in `outputs.json`/`published.json`.
  See Identity model; everything else in this document is a corollary of it.

## Identity model — the four layers

Everything here is a position in one hierarchy. Naming the layers explicitly is what makes the rest
*derivable* rather than arguable — it is the single answer to "what is the thing we are naming?"

```
Provenance layer    commit · branch · tag             where an output set came from
──────────────
Identity layer      certified output set              THE identity: outputs.json + published.json,
                    { archives, SHA256SUMS,           the per-artifact content digests, and the
                      signatures, … } certified       signatures — one manifest-defined collection
──────────────
Naming layer        v0.6.1 · dev-0420ec8 · latest-dev names that resolve to a release identity
──────────────
Projection layer    GitHub release · GitLab release   surfaces exposing a release identity on a
                    · registry tag · download URL      backend — never an identity themselves
```

**"Release identity" is a relation, not a surface.** Three concerns must stay distinct (an earlier
draft fused them, then over-promoted one to "the truth"):

- **Provenance** — origin: commit, source tree, review evidence. Authoritative store: Git.
- **Certification** — the *claim*: the artifact content-digests, bound, dated, anchored to their
  provenance commit, and signed (the birth certificate). Authoritative store: the signed manifest
  (`outputs.json`/`published.json` + signatures), recorded in Git.
- **Availability** — the actual bytes, somewhere retrievable. Authoritative store: the distribution
  backends.

The **artifact** — the verifiable thing a user downloads — is the **verification relation** across
them: `certification ⋈ resolved bytes ⋈ digest-equality`. Remove certification → nothing to check
against; remove bytes → nothing to check; remove the digest binding → no agreement. **No single
store owns this truth** — not StageFreight, not Git, not a forge, not a registry, not CAS. Truth
emerges when independently-stored evidence and independently-stored bytes *agree*; that multi-party
relation is the whole point (no store has to be trusted). Each store is authoritative only for *its
own* concern; none is authoritative for *the artifact*.

So where this document says "release identity," read **the certification record** — the signed
certified set, the *identifier* names resolve to and that you reason about, authoritative for the
*claim*. Adding a `windows-amd64` archive or an `sbom.spdx.json` changes it (the set changed). It is
**not** the artifact's truth surface: with every byte gone, the certified set is a signed statement
that something existed — the claim, not the thing. (Migration test: delete all bytes, keep Git → the
claim survives, the artifact does not. **Git is root-of-claim, not root-of-artifact.**)

Each layer's role, now a one-liner instead of an open question:

- **Names** (`v0.6.1`, `dev-0420ec8`, `latest-dev`) resolve to a release identity. A name derived
  from a commit (`dev-{sha:8}`) carries provenance *in the name* for readability — but it resolves
  to the output set, not the commit.
- **Aliases** are names. **Ref minting** creates names. **Forge materialization** creates
  projections. **None create identities.**
- **Projections** (forge releases, registry tags, download URLs) expose a release identity on a
  backend. Never an identity.
- **Retention** operates on certification records. **Signing** produces certification.
  **Verification** is the *relation* — it resolves a record's bytes and checks digest-equality;
  it does not "terminate at" any one store.

**Single-anchor invariant — the certification record is the single anchor *for identification*, not
a truth surface for the artifact.** The certification record is the *certified set*: the artifact
content-digests — bound, dated, anchored to their provenance commit, and signed (the birth
certificate). The digests are the **byte-identity**; the commit + timestamp are the **provenance
anchor**; both immutable, naming different things (one commit → many possible byte-sets, so the
commit dates and sources the bytes, it does not discriminate them). Born once, fixed thereafter, the
same record regardless of lifecycle or whether bytes are retained anywhere. It is **not** the commit
(provenance), **not** any storage location (Git, content store, forge — all infrastructure, each
authoritative only for its own concern), and **not** a forge release (projection). Names and
projections resolve to *the record*; the record is then verified *against resolved bytes* (the
relation). This is what keeps ref-minting honest: a ref names the certification record, never "a
release" and never "a store entry" — so no storage or projection layer can pose as the artifact's
authority. There is **no single truth surface**; there is a single *identifier* (the record) feeding
a multi-party verification relation.

**Identity vs availability are different claims.** The certification record persists *whether or not*
its bytes still exist somewhere. Distribution does not change the record — it imposes a *durability
obligation*: see the retention substrate, below.

### Durability contract — the governing project-level decision (NOT an invariant)

The four layers say what identity *is*; they do **not** say what StageFreight *promises to preserve*.
That promise is a distinct decision — the **durability contract** — and it, more than reproducibility
alone, determines whether any byte store is even needed. The persistence-identity algebra deliberately
does not make it: a `PersistenceHandle` is a *locator*, the trust anchor is
`re-hash(resolved bytes) == Artifact.Digest`, and no store is granted authority. So **"durable
retention is required" is not an invariant — it is a consequence of choosing Option B below.** Stated
unconditionally (an earlier draft did) it silently smuggles in Option B.

- **Option A — re-run from source (stateless).** Historical distributed identities are *not*
  StageFreight durable state. It produces and publishes; it promises nothing beyond what the pipeline
  regenerates. The content store stays a pure cache; the migration test passes (destroy store, move
  machines, re-run).
- **Option B — durable historical objects (stateful).** A previously distributed identity must remain
  reconstructable and verifiable independent of source. Requires a durable byte home and makes
  StageFreight a system-of-record (backups, retention obligations).

**The contract couples with reproducibility; only one cell needs a byte store:**

|                      | Option A (stateless)                                                                                          | Option B (durable store)            |
|----------------------|-------------------------------------------------------------------------------------------------------------|-------------------------------------|
| **Reproducible**     | ✅ historical identities *reconstructable from source*; no byte store; the manifest-in-Git carries identity   | redundant — you could rebuild       |
| **Non-reproducible** | ⚠️ historical identities **not** reconstructable; verify-later survives only at the distribution backend's discretion | ✅ exact bytes durably preserved (StageFreight becomes stateful) |

**DECISION (made): Option A — stateless orchestrator, externalized + mirrored retention.**
StageFreight projects the certified truth *outward* into the persistence backends (primary forge +
mirror forges + registries + optional CAS) and reconciles their retention against the declared policy
— exactly as it already does for images, now extended to binaries via forge APIs. It owns no durable
store, needs no backup, and the migration test still passes; the *bytes* live durably in systems
whose job is persistence, mirrored for redundancy, with a configurable primary in `.stagefreight.yml`.

This **corrects an under-specification** in the table above: *"Option A" does not mean "no retention /
regenerate-only."* It means *StageFreight* holds no durable state — retention is real, but externalized
and mirrored. So the non-reproducible + A cell is **not** best-effort backend discretion; it is
deliberate, redundant, policy-governed retention across backends (the earlier ⚠️ conflated "stateless"
with "no durable retention"). StageFreight is a **stateless reconciler**: desired state in Git
(`.stagefreight.yml`), actual state queried from backend APIs, reconcile → project → prune.

**Reproducibility is now a *second, independent* verification path** (rebuild → re-hash → match the
signed manifest), layered on the mirrored-retention path — the strongest supply-chain overlay and a
way to shrink reliance on retention, not the sole source of historical verifiability.

**Primary ≠ authority.** A configurable "primary" backend is the *canonical availability source* (and
the one mirrors replicate from), never an authority over truth: every fetched copy — primary or mirror
— is verified against the signed certificate (Invariant 6; the algebra's `re-hash == Digest`).
"Toggle the preferred truth source" toggles the primary *availability* target, not what is true.

### The substrate, given Option A

Identity is the Git-resident certificate, so referent bytes live in the **distribution backends
themselves** (forge releases, OCI/package registries) — exactly how docker images publish today.
StageFreight stays stateless; a backend controls *availability*, never *trust* (verification
terminates at the certificate). The content store is an **optional cache** (dedup, acceleration),
never a system-of-record; if it vanishes, nothing breaks. Losing bytes ends *availability*, never
identity (Invariant 7).

Do not fuse the two senses of "retention": the **retention policy** (`keep_last`/`prune`) operates on
*identities* and lives in the naming layer; the **retention substrate** holds *referent bytes* and
sits beside the hierarchy. The policy drives the substrate's lifecycle, but they are different things.

## The model

Three operational concepts, each a position in the hierarchy above.

### Release identity — the certified output set (identity layer)

The manifest-defined output collection, certified once and never mutated:

```
dev-0420ec8 ─┐                      v0.6.1 ─┐
  (a name)   ▼                       (a name)▼
        release identity                 release identity
        ├── linux-amd64.tar.gz           ├── linux-amd64.tar.gz
        ├── linux-arm64.tar.gz           ├── linux-arm64.tar.gz
        ├── SHA256SUMS                   ├── SHA256SUMS
        └── signatures                   └── signatures
```

The left-hand labels are naming-layer; the bundle is the identity. Provenance (the commit) lives
*inside* the certification, not in this diagram — it is a different layer. Bears all bytes and all
trust material; never mutated.

### Release alias — a name that resolves to a release identity (naming layer)

```
latest-dev → (release identity of dev-0420ec8)
```

Contains *exactly one thing*: the release identity it resolves to. **No bytes, no checksums, no
signatures, no provenance of its own.** An alias is a *name for* an immutable identity — never a
second object. The registry property (`latest-dev → digest`) preserved verbatim, and the same
family as [[persistence-identity]] / the content-store handle: naming and retrieval are not
distribution and carry no trust surface.

### Retention — operates on release identities

```yaml
retention: { keep_last: 5, protect: [latest-dev] }
```

means:

1. Resolve aliases → their target release identities.
2. Protect those identities.
3. Prune old identities, each taking its forge release **and** its git tag together.

A materialized alias (see Forge strategies) is a *projection*, not an identity — it is **excluded
from the `keep_last` counted set**, exactly as `latest-dev` is never one of the five counted
`dev-*` registry tags.

### Not defined here: what creates an identity

The model is **trigger-agnostic**. It does not require, mention, or privilege any event as the
source of identities. `every main push`, `every prerelease tag`, `nightly`, `post-review` — all are
ordinary `when:` scheduling policy, the same machine that drives every other StageFreight target.
Coupling the channel to "per-commit dev releases" would repeat the `edge` mistake one layer up:
policy masquerading as architecture. A per-commit dev channel, a nightly channel, and an RC channel
are the **same capability** under three different `when:` clauses (see Config shape), and the
document must keep them visibly identical.

## Enforced invariants (each is a test target, not just prose)

1. **Every name and every projection resolves to the exact same certified output set.** This is the
   strongest invariant in the model — the one the whole identity hierarchy exists to state.
   Verifying any artifact fetched through *any* name (`latest-dev`, `dev-0420ec8`, `v0.6.1`) or *any*
   projection (a GitHub release, a GitLab release, a download URL) is **bit-for-bit identical** to
   verifying the release identity itself — same archives, same `SHA256SUMS`, same signatures. Names
   and projections cannot change what verification sees; a consumer pulling `latest-dev` verifies
   the certified output set it names. A backend that cannot preserve this equality is not
   implementing StageFreight names/projections — it is shipping a different artifact under a borrowed
   name. First-class test target:
   `verify(fetch(name)) ≡ verify(fetch(projection)) ≡ verify(release-identity)`. Stronger than
   commit-identity (which cannot survive non-reproducible builds) and more precise than
   build-identity (a build is a process; the identity is its certified outputs).
2. **An alias originates no bytes.** The structural corollary of (1): whatever an alias serves is
   the target identity's bytes copied verbatim — never regenerated, never independently signed,
   never re-checksummed.
3. **Retention counts release identities, never names or projections.** `keep_last: N` keeps N
   certified output sets; the names that point at them (aliases, the rolling channel name) and the
   projections that expose them (materialized alias releases) are outside the count.
4. **Prune is whole-artifact.** Removing a release identity removes its forge release projection and
   its git tag atomically — no orphaned tags, no orphaned releases.
5. **Protect is by name.** Only user-named names (and the release identities they resolve to) are
   immune from pruning. Aligns with [[boundaries]] / [[invariants]].
6. **Single anchor *for identification* — names resolve to the certification record** (foundational).
   No storage or projection system *is* the identifier: Git holds provenance, the substrate holds
   referent bytes, forges project — names resolve to the **certification record** (the signed
   certified set), never to a Git-, store-, forge-, tag-, or manifest-resident object posing as the
   thing-being-named. Test target: resolving *any* name or projection yields the certification record.
   (This is the *identifier* anchor — not a claim that the record is the artifact's truth; see
   Invariant 10.)
7. **Identity persists independent of availability.** Losing referent bytes (GC, prune, forge-side
   deletion) ends an identity's *availability*, never its existence: the certified set remains as a
   record. Test target: a pruned identity is still resolvable *as a record* (its digests +
   certification) even when no bytes remain to download.
8. **Identity is Git-anchored and self-locating.** Because the certificate records its provenance
   commit, a found artifact is identifiable and datable with *nothing but Git*: hash it → its digest
   → the signed certificate naming it → the commit → Git places it in time, source, and content. No
   store, CAS, or forge is consulted to *identify or date* a found artifact — only to *fetch* one.
   Test target: given an artifact + its certificate and an offline clone, verification and dating
   succeed with every byte-store unavailable. (Resolving "what was in it" needs the Git object
   reachable — normal for tagged/released commits; an orphaned commit still yields hash + timestamp.)
9. **Where bytes persist is an open implementation choice.** The model requires durable retention of
   exact bytes (Invariant: distributed non-reproducible identities), *not* a particular store. The
   identity survives migration across persistence realizations — OCI layout, registry reference,
   object store, forge asset, CAS blob — exactly the [[persistence-identity]] stance: representations
   are validated against phase invariants and verify-on-read, never elevated to authority. Test
   target: swapping the persistence realization changes no name, projection, or verification outcome.
10. **Truth is a verification relation, not a surface** (foundational). The *artifact* is real iff
   `certification ⋈ resolved bytes ⋈ digest-equality` holds — a relation across independently-stored
   parties. **No single store owns it** (not Git, StageFreight, forge, registry, or CAS); each is
   authoritative only for its own concern (provenance / claim / availability). Test target: no code
   path treats any one store's say-so as sufficient — every consumer re-hashes resolved bytes against
   the record's digest before acting (the algebra's `re-hash == Digest`). Corollary (migration test):
   delete all bytes + keep Git → the *claim* survives, the *artifact* does not; Git is root-of-claim,
   not root-of-artifact.

## Forge materialization — a projection-layer *strategy*, not the model

Materialization lives in the projection layer: it creates a *projection*, never an identity.
Registries have a native alias primitive — a tag is a pointer at a digest, so `latest-dev` costs
nothing and moves nothing. Some forge backends (GitHub, GitLab) have **no native "release alias"** —
a downloadable `…/latest-dev/…` URL requires a release object at that ref.

A backend may therefore **materialize** an alias as a forge release projection. That is allowed
*only* under Invariant 1: the materialized projection re-presents the target identity's exact bytes
(copied verbatim, same checksums, same signatures), references the identity for provenance, and is
treated by retention as a projection (Invariant 3) — outside the `keep_last` count. On the next
channel build it is repointed (delete + recreate at the new identity) — still transparent, still no
trust surface.

This is documented as a per-backend capability with a conformance rule, deliberately **not** baked
into the conceptual model. Modeling "GitHub releases can't alias assets" as architecture would make
the abstraction narrower than the problem the first time a new forge or distribution backend is
added. The model is "alias resolves to identity"; materialization is how a limited backend honors
it.

## Config shape — one machinery, any trigger

The identity / alias / retention fields are the capability. The `when:` clause is orthogonal
scheduling. The same three channels the user may want are visibly the same target with a different
`when:`:

```yaml
# per-commit dev channel
- id: dev-binaries
  kind: release
  prerelease: true
  tag: "dev-{sha:8}"
  aliases: ["latest-dev"]
  archives: stagefreight-binaries
  retention: { keep_last: 5, protect: ["latest-dev"] }
  when: { branches: [main], events: [push] }

# nightly channel — same machinery
- id: nightly-binaries
  kind: release
  prerelease: true
  tag: "nightly-{date}"
  aliases: ["nightly"]
  archives: stagefreight-binaries
  retention: { keep_last: 7, protect: ["nightly"] }
  when: { schedule: [nightly] }

# RC channel — same machinery
- id: rc-binaries
  kind: release
  prerelease: true
  tag: "rc-{version}"
  aliases: ["preview"]
  archives: stagefreight-binaries
  retention: { keep_last: 5, protect: ["preview"] }
  when: { git_tags: [prerelease], events: [tag] }
```

Identity, alias, and retention are identical across all three; only `when:` and the user's chosen
patterns/names differ. If the document ever makes one of these look like a distinct feature, the
abstraction has leaked.

## Backing-ref minting — a naming-layer *strategy*, not the model

Ref minting lives in the naming layer: it creates a *name*, never an identity. A forge release
object is anchored to a ref. When the trigger that mints an identity is itself a ref (a pushed
`rc-{version}` tag), the anchor already exists. When it is **not** — a branch push, a schedule —
the backend must **mint** a backing ref for the new identity (e.g. an internal `dev-{sha:8}` tag)
so the release projection has something to hang on.

This is the structural twin of Forge materialization: a backend primitive that exists only to
satisfy a forge's anchoring requirement, introducing no new identity and no new trust surface. It
is documented as a per-backend capability with a conformance rule, deliberately **not** part of the
conceptual model — the model says only "an identity exists"; *how* a ref-less trigger gets a ref is
the backend's problem. Conformance rules:

- **The ref is a carrier, not the identity.** Identity is the certified output set (its artifact
  digests + `SHA256SUMS` + signatures) — never the ref. The ref names and anchors; deleting or
  recreating it (on prune, or when re-pointing an alias) changes *distribution*, never *identity*.
  Same family as [[persistence-identity]] / the content-store handle: a handle locates bytes, it is
  not their truth.
- **The ref resolves to the exact built commit.** `dev-{sha:8}` points at the `sha` it encodes. A
  minted ref *is* provenance, so it is never floating, approximate, or reused across commits.
- **Minting is idempotent per identity.** Re-running the same trigger for the same `sha` resolves
  to the same identity, never a divergent one — same input commit → same `dev-{sha:8}` → same
  bytes, so Invariant 1 holds across re-runs as well as across aliases.
- **A minted ref is not release lineage.** An auto-minted channel ref must never be mistaken for a
  human release tag or pollute stable version lineage. **Traced (2026-06):** the `tag_sources`
  search path and the changelog boundary (`PreviousReleaseTag`) are already safe by construction —
  both are *pattern-scoped* to the declared semver `tag_sources`, and a non-semver `dev-{sha:8}` ref
  matches neither those patterns nor `semverRe`. No `tag_sources` exclusion is needed; naming
  discipline (channel refs are non-semver) carries it. The **actual** exposure is the *unfiltered*
  `gitstate.ExactTagAtHEAD` ref scan, which a freshly-minted ref sits squarely in the path of:
  - `headAtTag` (gitver.go:216) — if one commit carries *both* a release tag and a channel ref,
    the scan may return the channel ref and misclassify a real release-at-HEAD as a dev build.
  - `ci/context.go:74` — local detached-HEAD fallback could read the channel ref as the trigger
    (CI is unaffected: `SF_CI_*` env is authoritative there).

  **Implementation-time guard (test target):** `ExactTagAtHEAD` and its callers must ignore channel
  refs — either restrict the scan to `tag_sources`-matching refs, or exclude the channel's minted-ref
  namespace. This is the one place ref minting reaches into another subsystem; the guard is narrow
  and precise, not a broad `tag_sources` change.
- **Never surfaced as policy.** The user asked for "a rolling channel, keep the last N." The minted
  ref is how a forge backend honors that — documented here, never presented to the user as
  "StageFreight tags every commit."

Registries need none of this: a registry channel addresses content by digest and never anchors to a
ref, so backing-ref minting is a forge-shaped concern only — further evidence it belongs in the
backend layer, not the model.

**The one mechanical consequence** of trigger-orthogonality: when an identity is minted by an event
that is *not itself a ref* (a push, a schedule), the forge backend must mint a backing ref for the
identity — the structural twin of alias materialization (above), bound by the same "no new trust
surface" discipline and given its own conformance rules in **Backing-ref minting** below. This is a
backend implementation detail, never a property of the channel model, and never surfaced to the
user as "we tag every commit."

## Gaps in current code (verified — what implementation must close)

- **Retention + rolling aliases are primary-only.** `findPrimaryReleaseTarget` returns the *first*
  non-remote `kind: release`, and both the auto-tag block and the retention block read from that
  one target (`release_create.go` ~613 / ~751). A `dev-binaries` channel needs per-target
  retention/aliases.
- **Prune leaves the git tag.** `forgeStore.Delete → DeleteRelease` only (`release_prune.go:187`);
  GitHub `DELETE /releases/{id}` and GitLab `DELETE /releases/{tag}` both leave the ref. Invariant
  4 requires also deleting the tag (`DeleteTag` exists, used by the rolling-alias path ~631).
- **Identity creation is tied to `events:[tag]`.** The release path runs only when the triggering
  event is itself a ref. The channel model is trigger-agnostic, so identity creation must be
  drivable by any `when:` (push, schedule, tag), with the backend minting a backing ref when the
  event is not one. This is the orthogonality gap, not a "dev channel" feature.
- **`binary-archive` is stable-gated.** Its `when:` must include the dev/prerelease trigger so the
  archives exist to attach.

## Reserved (DESIGN ONLY; not in this capability)

- **Channel across non-forge distribution backends** (object store, generic package registries).
  The model is intended to extend here unchanged — identity + alias + retention — but each backend
  ships separately with its own materialization conformance.

## Self-update freshness hint — a deferred *consumer* of channel resolution (UX, not security)

Surfaced by the 2026-06 replay-corruption incident, whose root cause was simply: *an operator
unknowingly ran an outdated-but-valid executable lineage against a newer repository lineage.* This is
**advisory UX, not correctness infrastructure** — distinct from the TUF anti-rollback goal below
(that is security). The correct model is one question, nothing more:

> *Am I executing a build that is behind my configured publish-origin lineage for my resolved release channel?*

Not "do I contain capability X?" (build/CI guarantees that — runtime self-interrogation is
self-referential), not "is there a newer version anywhere?" (a parallel update mechanism). The
boundary is a single trivial primitive — resist turning it into a capability framework, release
taxonomy, provenance subsystem, or runtime policy layer:

```go
func MaybeWarnOutdatedBinary(ctx PublishContext) error
//   1. resolve configured publish-origin (from the binary's embedded {origin, channel, commit})
//   2. resolve the effective update lineage (publish-origin DEFAULT-BRANCH HEAD — tags are
//      snapshots; HEAD is the live moving target, e.g. latest-dev *is* default-branch HEAD)
//   3. compare embedded build lineage vs remote lineage, within the resolved channel only
//   4. emit an advisory warning if behind
//   5. silent no-op on: no network · no publish-origin · unresolved lineage · unstamped build ·
//      forge failure · ambiguous config
```

Hard constraints, each peeled away during design:

- **Channel comes from config, never from the version string.** Inferring `stable` vs `dev` from
  `vX.Y.Z` vs `dev-<sha>` bakes an invariant `.stagefreight.yml` can invalidate. Channel =
  `ResolveReleaseChannel(cfg)` or **absent → silent no-op, no fallback heuristics**. Cross-channel
  suggestions forbidden (a `vX.Y.Z` user is never nudged to a `dev-<sha>`).
- **The keystone — whose config?** It is about **StageFreight's own** publish-origin, not the cwd
  project's `.stagefreight.yml`. So StageFreight's build embeds *its own* `{publish-origin, channel,
  commit}` into the binary (beside `version.Commit`); the runtime reads the embedded values and works
  anywhere. The one *real* consumer of an embedded provenance manifest — not capability-marker theater.

Properties: **publish-path-gated** (before push/replay/docker-push — never every command, never a
startup assertion), **best-effort**, **silent on failure**, **advisory only**, **disposable** if it
becomes annoying. TTL is UX, never correctness.

**Deferred deliberately:** depends on `ResolveReleaseChannel` (this capability) *and* a build-embed
step. Building before either exists yields an inert stub or the banned version-string heuristic — so
it ships *with* release-channels, ~15 lines, not before.

## Striven-for goals — deferred is not abandoned (the high-assurance trajectory)

These move StageFreight from *on-par* to *best-in-class* on supply chain. They are **explicit,
committed goals**, each already designed or reserved in an adjacent doc — not gaps we dropped. They
layer onto the relation model (Invariant 10) without changing it; each strengthens one party of
`certification ⋈ resolved bytes ⋈ digest-equality`.

1. **Keyless signing + transparency log (Rekor).** *Strengthens certification.* Tamper-evident,
   no-single-trusted-party signing — the natural endpoint of "truth is a relation, no store is
   trusted." Already scaffolded in [[signing-trust-model]]: `keyless→oidc` is a first-class method,
   the schema already carries `TLog bool`, and full Rekor claim verification is its reserved
   verification phase. **Pull forward early** — cosign keyless writes to Rekor nearly for free, and
   it is the cheapest large step toward the no-authority property this model is built around.
2. **Reproducible builds (bit-for-bit).** *Strengthens the bytes side.* Enables the second
   verification path (rebuild → re-hash → match) and shrinks reliance on retention. Already *measured*
   by crucible (`TrustReproducible`) and central to [[multi-arch-strategy]]; [[signing-trust-model]]
   reserves reproducible-build metadata so a third party can independently re-derive the digest.
   Concrete first step: kill wall-clock nondeterminism (`{date}=time.Now()` in `engines/binary.go`,
   image `BUILD_DATE`/created) → commit timestamp / `SOURCE_DATE_EPOCH`; pin toolchains. **Overlay,
   never a gate** (do not block channels on it).
3. **Key rotation / threshold / TUF-grade roles.** *Strengthens certification's longevity.* The
   long-horizon high-assurance target (revocation, multi-party threshold, freshness/anti-rollback,
   role separation). Already partly handled: [[signing-trust-model]] treats key-rotation overlap as
   an invariant-confirming case ("old key vs new key signed this must stay answerable"). Full
   TUF-style roles + anti-rollback for rolling channels (`latest-dev` freshness) remain reserved —
   deliberately *after* (1) and (2), but **on the roadmap, not off it**.

Sequencing: (1) and (2) are near-term and cheap relative to their value; (3) is long-horizon. None
is abandoned; all are tracked here and in their home docs.

## Design framing & watch-points

- Do not model GitHub/GitLab limitations as the abstraction. The conceptual boundary is *alias →
  immutable identity, zero trust surface*; forge materialization is a subordinate strategy bound by
  the conformance invariants.
- Keep the registry↔release symmetry total: anything true of `latest-dev`-the-tag should be true
  of `latest-dev`-the-alias, except the one backend-local fact that a forge may have to materialize
  it.
- Naming discipline (`dev-{sha:8}` everywhere) is what makes the cross-artifact mental model hold;
  resist re-introducing `+` into refs for "semver correctness" — it belongs in the filename.
- **The trigger is not the channel.** Watch for "dev releases on every push" creeping back into
  the model's definition, examples, or invariants. Per-commit / nightly / RC are `when:` policy
  over identical machinery; if any reads as a distinct feature, policy has leaked into the
  abstraction — the same failure as hardcoding `edge`, one layer up.
- **`verify(name) == verify(projection) == verify(release-identity)` is the load-bearing
  invariant.** Every backend strategy (alias materialization, ref minting) is judged against it.
  Treat it as the first thing a new distribution backend must prove, not a footnote.
- **Forge- and topology-agnostic — "public" is operator config, not architecture.** Which backend is
  the public release source vs an internal mirror, which is `primary`, and whether a self-hosted forge
  is exposed on 443 — all the *operator's* deployment choice, never hardcoded. Release-asset-style
  delivery (clean permalinks) is a capability *every* major forge has — GitLab
  `/-/releases/<tag>/downloads/<file>` via `direct_asset_path`, GitHub `releases/download/<tag>/<file>`,
  Gitea/Forgejo equivalents — and StageFreight emits it **uniformly** across configured backends,
  preferring none. Tokenless = a function of project visibility + instance exposure, set by the
  operator (a public GitLab on 443 is a perfectly valid *sole* public authority). Baking "GitHub is
  public, GitLab is internal" into the model is the `edge` mistake again: deployment policy posing as
  architecture. StageFreight has many operators; the model serves all topologies without preference.
  (And per Invariant 10, even a "public release authority" is the public *availability* source, never
  a truth authority — verification still terminates at the certificate.)
- **Layer hygiene.** A name is not an identity; a projection is not an identity; provenance is not
  an identity. Watch for any of the four collapsing into another — a forge release treated as the
  identity, a commit treated as the identity, an alias treated as a second object. Every such
  collapse is the same category error (`edge`-as-architecture, release-as-identity) at a different
  layer. The identity is the certified output set, always.
- **Identity *equality* — decided in principle by the single-anchor invariant; only the
  serialization spec remains.** Because identity is the certified digest set (Invariant 6), equality
  *is* equality of that set — a **content digest over the canonically-serialized certified set**
  (the signed manifest), independent of any storage location. The earlier A/B ambiguity (structural
  object vs content digest) collapses: it is the digest (B), computed over the structure (A). What
  remains is purely the *serialization spec* — the canonical-bytes rule for the certified set, riding
  `intent_checksum` and the determinism-from-types discipline ([[schema_determinism_from_types]]).
  Settle that spec in the staging plan; the *model* question is closed.
