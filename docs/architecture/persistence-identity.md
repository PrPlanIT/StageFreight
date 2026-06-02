# StageFreight — Persistence Identity Algebra

This document is the correctness model for how an artifact's bytes are carried
across the perform → review → publish phases. It exists because a stress-test
(designing a hypothetical third content store) revealed a **false isomorphism**
in the implementation: two independent axes had quietly fused into a 1:1
mapping, and the system depended on that coupling without declaring it.

It is design-level: it states constraints a future implementation must satisfy,
not code that exists today. Where it describes current behavior, that behavior is
in `src/cas`, `src/artifact` (PersistenceHandle), `src/cli/cmd/publish_promote.go`,
and the review resolver in `src/cli/cmd/security_scan.go`.

---

## The three layers

The artifact-transport system has three layers that must stay coherent:

1. **Capability layer — what operations a store allows.**
   `cas.Store.Transport()` (policy: is cross-phase transport active?) and
   `cas.Store.RequiresOCIExport()` (mechanism: must perform emit an OCI layout?).

2. **Persistence identity layer — what the system believes exists, and how to
   reach it.** `artifact.PersistenceHandle`. Today this has exactly one
   inhabited variant: `OCILayout{Path}`.

3. **Phase authority layer — who may assert truth.** perform produces, review
   validates, publish asserts external effects + records outcomes.

---

## The invariant that was never declared

The implementation silently assumes:

```
Store capability  ──maps 1:1──>  Persistence representation
```

i.e. "if a store is valid by capability, its persistence is an OCI layout with a
filesystem path." Every consumer encodes this by reaching
`a.Persistence.OCILayout.Path`:

- review: `resolveCASTarget`
- publish: `promoteArtifacts`
- the activation / bridge / e2e tests

The **correct, stronger invariant** — the one the system actually depends on — is:

> **Every valid capability combination must be representable in the
> persistence-handle algebra, and that representation must survive transformation
> across all phase boundaries.**

Capability tells you *what operations are allowed*. PersistenceHandle tells you
*what the system believes exists*. The law is that these stay **coherent under
transformation across phases** — not that capability and representation are the
same thing.

Stated negatively: capability validity is **not** the system boundary.
*Representational compatibility* is.

---

## Where the isomorphism breaks

The capability quadrant doc (`src/cas/cas.go`) declares
`Transport=true, RequiresOCIExport=false` a valid future store — e.g. a
`RegistryStore` where perform pushes the candidate to an internal staging
registry and the handle is a registry digest reference, not a filesystem path.

That store is **valid by capability** but **unexpressible by handle**: there is
no `PersistenceHandle` variant for a registry reference. So:

- a `(Transport=true, RequiresOCIExport=false)` store cannot be built today
  without **also** extending `PersistenceHandle`, in lockstep with the capability;
- nothing in code or tests states this coupling. It is the gap.

The missing type is not "RegistryStore." It is the missing variant in the
handle algebra:

```
PersistenceHandle :=
    OCILayout(path)
  | RegistryRef(host, digest)     // not yet — and may not be first-class; see below
  | (future variants…)
```

### Why only a registry-shaped store reveals this

Three hypothetical stores, only one is truth-revealing:

| Store         | Quadrant            | What it does to the model |
|---------------|---------------------|---------------------------|
| RemoteCAS     | (true, true)        | Cheats — stays OCI-shaped (handle is still a layout path after fetch). Reveals a *timing/error* gap (below), not a representational one. |
| MultiTarget   | (true, true)        | Orthogonal cardinality — composes cleanly; the results layer already records multiple outcomes per artifact. Confirms multi-target correctness rather than breaking anything. |
| **RegistryStore** | **(true, false)** | **Introduces a new identity type for persistence itself.** Forces the admission that "layout path" is one *realization* of persistence identity, not the universal handle. This is the only one that breaks representation. |

---

## The open question a second store must answer (do NOT pre-decide)

Before adding a `RegistryRef` variant, the system must learn — by running a real
second store through **all** phase boundaries (review verification, publish
promotion, results binding) — whether a registry reference is:

- a **first-class persistence identity** (a genuinely different kind of "what
  exists," verified and promoted by its own path), or
- merely a **transport encoding of `OCILayout`** (the same OCI bytes, addressed
  differently, that normalize back to a layout before any consumer touches them).

That distinction is only visible at the phase boundaries. Adding the variant
before knowing the answer would be fictional abstraction pressure. The probe
(a real store) decides it; this document does not.

---

## Phase-transformation constraints (the correctness model)

A persistence handle is produced in perform and consumed in review and publish.
The allowed transformations:

| Phase   | May do to a handle | May NOT do |
|---------|--------------------|------------|
| perform | **create** a handle from bytes it just produced; the handle's identity MUST equal `Artifact.Digest` (verified by re-hash on first store) | create a handle it cannot itself verify; create one whose representation no consumer can resolve |
| review  | **resolve + re-hash** the handle to its bytes and evaluate them; on failure, fall back loudly | trust a handle without re-hashing; mutate or re-encode it |
| publish | **resolve + re-hash**, then distribute the exact bytes, then record the observed outcome in `published.json` | rebuild; distribute bytes whose handle it did not verify; transform identity during distribution |

The cross-cutting rule binding all three:

> **A handle is never trusted as a bare claim. Every consumer re-hashes the
> bytes it resolves against `Artifact.Digest` before acting.** (`cas.VerifyLayoutAt`
> is the current realization; a new variant must provide an equivalent
> verify-on-read or it is not a valid persistence identity.)

This is why a new variant is not free: it must supply a verify-on-read that
proves `resolved bytes == Artifact.Digest`, in **whatever representation it
uses**, or it breaks the one rule the whole trust model rests on.

---

## The second latent gap (error algebra)

A network-backed `Resolve` (e.g. object-storage CAS) exposes a gap the
filesystem store never does: the error vocabulary is `ErrNotFound` /
`ErrIntegrity` only — there is **no "transiently unavailable, retry" category**.

Under the current review resolver, a transient network failure to resolve a
handle is indistinguishable from "no handle," so review would **fall back to the
legacy publication-derived path** — silently degrading the trust guarantee on a
blip. Any store whose `Resolve` can fail transiently MUST introduce a transient
error category, and review MUST treat transient-unavailable as *retry/hard-fail*,
never as *fall back to a weaker trust path*.

---

## Capabilities encoded in types, validated as values

A conceptual note that explains why `cas.AssertStoreCapabilities` exists and
feels redundant today:

- The current stores **encode** capability in their types — `FSStore`/`NoopStore`
  return compile-time constants from `Transport()`/`RequiresOCIExport()`.
- `AssertStoreCapabilities` **validates** capability as a runtime value.

That duality is why the function is enforcement at the only ingress (`execute.go`)
but feels like it has nothing to guard: for compile-time-constant stores it is a
pre-boundary validation hook — legitimate, just early. It becomes load-bearing
when a store decides capability at runtime. At that point enforcement should move
toward the **construction/registration boundary** (which does not exist yet);
`execute.go` is today's only shared *ingress*, not the natural *enforcement
boundary*, and authority should not be mislocalized there permanently.

---

## What this document obligates

When a second content store is added:

1. Determine — by running it through review, publish, and results binding —
   whether its persistence is first-class identity or an encoding of `OCILayout`.
2. If first-class: add a `PersistenceHandle` variant **and** a verify-on-read for
   it, in lockstep with the capability that needs it.
3. If its `Resolve` can fail transiently: add a transient error category and make
   review hard-fail (never weaken) on it.
4. Move capability enforcement to the construction/registration boundary that the
   pluggable store introduces.

Until then, the OCI-layout-only handle is correct and sufficient — but it is
correct because there is one store, not because one variant is universal.
