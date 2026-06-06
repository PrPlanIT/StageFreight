# Release-Channel Materialization Contract — Projecting Certified Identities onto Forges

> **Status: living design document.** The backend contract for the projection layer of
> [[release-channels]]. It answers exactly one question: *how does a forge expose a name pointing at
> a release identity without becoming a second source of truth?* Iterate it *here*.

> **Availability model: LOCKED — Model C (published-artifact availability); StageFreight stays
> stateless.** Distributed non-reproducible identities require their certified bytes to remain
> *available* for later verification — but **StageFreight does not own those bytes.** Identity is the
> Git-resident signed certificate ([[release-channels]] Invariant 6); the referent bytes are held by
> the **distribution backends themselves** (forge releases, OCI/package registries, object stores),
> exactly as docker images are published today. A backend controls *availability*, never *trust* —
> verification always terminates at the Git-resident certificate. The content store stays a **cache**
> (acceleration, dedup), never a system-of-record. Forge releases are **projections only**; GitHub
> asset duplication is an *optimization*. **How long bytes survive is the durability contract's call**
> ([[release-channels]] — *not* an invariant of this contract; recommended Option A + reproducibility,
> under which verify-later works by *rebuild*, not guaranteed retention). Rationale below.

> **Implementation is gated.** Prerequisite is the in-flight signing / birth-certificate work (a
> Git-resident *signed* identity certificate), **not** a durable byte store — see Prerequisite. This
> document existing is not a trigger to build.

## Where this sits

[[release-channels]] settled the conceptual model: a four-layer hierarchy (provenance / identity /
naming / projection) whose identity is the **certified output set**. This contract specifies the
**projection layer** for forge backends — the riskiest piece, because it is where Invariant 1
(`verify(projection) == verify(identity)`) either holds uniformly or quietly fractures.

## The retention substrate (durability infrastructure, not a byte-home-of-identity)

Two things must be kept distinct (an earlier draft conflated them):

- **Identity** is the certified set — held by no storage system in particular; recorded in its
  certification ([[release-channels]] Invariant 6). Losing bytes does not destroy it (Invariant 7).
- **Availability** is whether the certified *referent bytes* can still be retrieved and verified.

Distribution of non-reproducible builds imposes a **durability obligation** on availability only:
the exact bytes a consumer downloads cannot be re-derived (`time.Now()` in `engines/binary.go:208`),
so they must be durably retained for later verification. That is the **retention substrate's** job.

**The durability obligation does not require StageFreight to own the bytes.** Because identity is the
Git-resident certificate, not a byte location, the referent bytes may be held by the **distribution
backends themselves** — GitHub releases, OCI/package registries, object stores already persist
immutable artifacts. StageFreight produces bytes ephemerally and publishes them into systems whose
job is persistence (the existing docker-image model), and stays **stateless**.

This does **not** reintroduce distributed truth. The earlier worry — *forges diverge, forges enter
the trust boundary* — assumed the forge copy was itself the truth. It is not: the **certificate (the
signed certified-set digest, Git-resident) is the single arbiter**, and every backend copy either
re-hashes to it or is rejected. Many backends may each hold availability with zero divergence in
truth, because truth is the certificate, not any backend's bytes. A backend's lifecycle controls
*availability*, never *trust*; verification of any bytes you hold always terminates at the
certificate. So a forge is an **availability provider, not a trust dependency.**

The **content store therefore stays a cache** (acceleration, dedup) — never a required
system-of-record. If it vanishes, nothing breaks: the certificate (Git) and the published bytes
(backends) still verify. Identity equality is equality of the certified-set digest (the signed
manifest) — a Git-resident property, not one any storage location confers.

**Where the bytes live is an open implementation choice — the model does not pick one.** OCI layout,
registry reference, object store, forge asset, CAS blob: the identity survives migration across all
of them. This is the [[persistence-identity]] stance verbatim — a `PersistenceHandle` is how a phase
*reaches* bytes to verify against `Artifact.Digest`, never the identity; representations
(`OCILayout` today, `RegistryRef` deliberately left open) are validated against phase invariants and
verify-on-read, never elevated to authority. The contract's only durability claim is the invariant
*"distributed non-reproducible identities require durable retention of exact bytes"* — **not** "in
the content store."

**The certificate is Git-anchored, so a found artifact is self-locating** ([[release-channels]]
Invariant 8). Because the certified set records its provenance commit, hashing any artifact yields a
digest that, via its signed certificate, names a commit Git can date and contextualize — with no
store, CAS, or forge consulted. Persistence governs *fetchability*; identity and provenance are
already complete in the proof + Git. That is the deepest reason a forge is an availability provider,
not a trust dependency: identity never needed the store to begin with.

## The materialization rule (the whole contract, in one sentence)

> A projection may **reference** canonical bytes or **copy** canonical bytes — but it must never
> **originate** them.

Everything below is the consequence of that rule plus per-backend capability differences. There is
no per-forge ontology: GitHub is not special, GitLab is not special; they differ only in *projection
mode*.

## Conformance (each a test target)

- **C1 — Verification equality.** `verify(projection) ≡ verify(identity)`, bit-for-bit: same
  archives, same `SHA256SUMS`, same signatures. The acceptance criterion. (Invariant 1 of the parent.)
- **C2 — No second trust root.** A projection never re-signs, re-checksums, recompresses, or emits
  its own `SHA256SUMS`/signatures. It carries the identity's trust material **verbatim**.
- **C3 — Originates no bytes.** Every byte a projection serves traces by digest to the canonical
  store. A copy-mode projection is a *mirror that must re-hash to the store digest*, not a new artifact.
- **C4 — Disposable lifecycle.** Creating, repointing (alias move), or deleting (prune) a projection
  never touches identity. Identity is created and destroyed **only in the store**; forges are
  render surfaces. (This is what makes "prune is whole-artifact" coherent — see Retention below.)
- **C5 — Provenance link.** A projection references the identity it projects (the store handle /
  identity ref), so a reader can resolve projection → identity → certification.

## Projection modes — the only backend axis

Two modes; conformance is identical across both. The mode is a *capability* of the backend, not a
property of the model.

| Mode | What the projection holds | Backend fit | Cost |
|---|---|---|---|
| **Reference** (lean, preferred) | links to canonical bytes in the store | GitLab (asset = link), object-store URLs, package registries | none — bytes live once |
| **Copy** (fat, optimization) | verbatim duplicate of canonical bytes | GitHub (asset = blob) | storage + write-amplification; a *mirror*, not a truth |

Reference mode is the native happy path. Copy mode is permitted **solely** as a UX optimization
(native `releases/download/{tag}/…` URLs) and only under C2/C3 — the copy re-hashes to the store
digest and carries the store's checksums/signatures unchanged.

## Backend mapping (grounded in the current forge layer)

- **GitLab** — `UploadAsset` already uploads to project storage then *links* to the release
  (`forge/gitlab.go`). Native **reference mode**: an alias release links the identity's canonical
  bytes; nothing is re-stored. The happy path.
- **GitHub** — `UploadAsset` POSTs *blob* bytes to `/releases/{id}/assets` (`forge/github.go`);
  release assets cannot be pure links. Two conforming options:
  1. **Copy mode** (recommended GitHub default): duplicate the canonical bytes as release assets —
     verbatim, same `SHA256SUMS`/signatures — to preserve native asset-URL UX. Explicitly a mirror
     of the canonical store, governed by C3.
  2. **Reference-in-body**: no native assets; the release body carries canonical store URLs. No
     duplication, at the cost of the `releases/download/...` convention.
  Both conform. The choice is UX (native URL vs zero duplication), never correctness.
- **Future backends** — reference mode if linkable, copy mode if blob-only. No new ontology; only a
  mode selection.

## Retention under this contract

Because identity lives only in the store (C4), retention is globally coherent and free of
split-brain: it operates on **store identities**; forge projections are torn down as a *consequence*
of pruning an identity, never as the locus of deletion. A GitHub blob deleted by GitHub's own
retention is a lost *mirror*, not a lost identity — re-materialization restores it from the store.
This is what makes the parent's "prune is whole-artifact" rule implementable without trusting forge
delete semantics.

## Supplementary verification assets (binaries need the scaffolding images get for free)

An OCI image is content-addressed end to end: the registry serves it by digest, a runtime verifies
`image@sha256:…` before scheduling, and cosign signs the digest. A binary is opaque bytes with none
of that native machinery. So the projection's job for binaries is to **publish the scaffolding images
get natively**, moving the verification burden off the developer and the consumer:

- **`SHA256SUMS`** — integrity of every archive.
- **Detached signatures** (cosign/minisign) over the archives and over `SHA256SUMS`.
- **The signed manifest** — every distributed item's digest + the **commit** + timestamp (the
  certificate itself, pullable and inspectable).
- **Provenance / attestation** (SLSA-style; the `results.go` `AttestationRef` slot already exists).
- Optionally a **verify helper / instructions** so a non-expert verifies in one command.

The goal is *delegable accountability*: a consumer (or an admission controller / IaC pipeline) who
retains these assets can verify signatures, confirm the commit, and **detect tampering over time
using standard tooling with StageFreight nowhere in the loop**. That is the orchestrator posture —
project the means of verification outward; never be the thing that must be trusted or online.

## Prerequisite (Model C — much smaller than an earlier draft claimed)

> **Retraction.** An earlier draft named a "blob-capable content store" as the *#1 gating
> dependency* and called release channels "downstream of a persistence-layer extension." That was
> Model-B-shaped (store-as-system-of-record) and is **withdrawn**. Under Model C, StageFreight owns
> no durable bytes, so no blob store is required.

The real prerequisite is **recording identity, not storing bytes**:

- **A Git-resident *signed* identity certificate.** `outputs.json` must become the certified set
  with signatures over the artifact digests — the in-flight birth-certificate + signing work. This
  is what every name and projection resolves to and what verification terminates at. It rides Git;
  it adds no durable state of StageFreight's own.
- **Publishing archives to the distribution backends** as the availability layer. The forge layer
  already does this (`forge/github.go`, `forge/gitlab.go` `UploadAsset`); binary archives now build
  to `.stagefreight/dist/` and are published like any other asset.

The content store (`cas`, currently OCI-layout-only) stays an **optional cache** — useful for dedup
and re-publish acceleration, never required for correctness. Extending it to generic blobs is a
*performance* option, not a gate. The architectural line is explicit: **StageFreight remains a
stateless build/publish orchestrator, not an artifact-authority-and-retention system.**

## What choosing this unlocks

- **Identity equality** (the parent's deferred watch-item) resolves to **store-digest equality** —
  deterministic and centralized, riding the determinism-from-types discipline
  ([[schema_determinism_from_types]]).
- **Materialization becomes a pure mapping problem** — reference vs copy, nothing else. No
  per-forge truth semantics.
- **Verification terminates once**, in the store; projections only re-check the same bytes.

## Reserved / open (DESIGN ONLY)

- **Byte-home substrate.** Extend `cas` to generic content-addressed blobs vs adopt a
  package-registry / object-store byte home. Separate decision; both satisfy this contract.
- **Release-identity equality digest.** The concrete digest over the canonically-serialized
  certified set. Home is now clear (the store); spec deferred to the staging plan.

## Acceptance

The contract is satisfied when one conformance suite — `verify(projection) ≡ verify(identity)`
across a reference-mode backend (GitLab) and a copy-mode backend (GitHub) — passes, with the content
store as the sole byte origin (C3) and no projection emitting its own trust material (C2). At that
point the `Commit 1…N` staging plan in [[release-channels]] derives mechanically.
