# StageFreight ↔ mirror integration — canonical spec v0.1

*Mara, `@io/stagefreight` consumer-side spec, 2026-06-22, commissioned by
Alex via Reed. Cross-repo cascade tick (continues from mirror PR #1's
substrate-decl preservation; this spec opens the consumer-side
implementation track in the StageFreight Go repository).*

*Discipline: substrate-pull-correct preservation. The mirror side has
landed the typed contract (`@io/stagefreight` family-root + `/narrative`
species + `docs/specs/stagefreight-wire-v0.1.md` + Seam adversarial
review). The StageFreight Go repository is the consumer/server side of
the same wire-protocol contract. This spec preserves what the mirror
cascade points at, names what the Go side will need to implement, and
gives the next ticks something to land against without committing to
shapes the mirror substrate-decl might revise later.*

*Honest hedges throughout. Forward-promises named. The PR this spec
describes is intentionally scoped to consumer-side substrate-decl
preservation — a thin adapter shape that survives mirror-side
refinement, not a Rust-realisation mirror.*

---

## §1. Recognition + context

### 1.1 Why this spec lands now

On 2026-06-22 the mirror substrate landed the `@io/stagefreight` family
on `mirror/main` (PR #1, "StageFreight substrate-decl v0.1 (preservation
tick; realisation forward-promised)"). Five artifacts shipped:

```
shards/io/stagefreight.mirror              family-root (consolidated)
shards/io/stagefreight/narrative.mirror    prose-projection species
docs/specs/stagefreight-wire-v0.1.md       canonical spec (Mara, 1535 lines)
docs/audits/stagefreight-seam-review-      Seam adversarial review trail
  2026-06-22.md                             (3 TIGHT findings closed)
docs/releases/                              release notes
  stagefreight-v0.1.0-substrate-decl.md
```

The mirror side now carries the typed contract: settled crystals (from
the kintsugi loop) become wire-addressable via `spectral_coordinate`,
ship via `freight()`, and project to prose via the `/narrative` species.
What the substrate-decl deliberately does NOT do is implement the
bytes-on-wire layer. The realisation gates (Phase 1 boot-grammar zero
holonomy + Phase 4b emitter discharge + crystal substrate-decl task
#268) keep the Rust side intentionally forward-promised.

This is the substrate-pull-honest point at which the consumer side
opens. StageFreight (the Go binary, this repository) is the consumer
the wire protocol talks to — the server that receives `freight()`'d
crystals at their `spectral_coordinate` addresses, validates the
typed contract, and stages them for downstream lifecycle phases.

### 1.2 What this PR is, and what it is not

**This PR lands the SPEC. The Go adapter package the spec describes is
the forward-promised next tick (PR-B).** PR-A ships
`docs/architecture/mirror-integration-spec-v0.1.md` as the typed
contract; PR-B ships `internal/stagefreightmirror/*.go` as the
implementation. See §11.6 for the explicit cascade order.

This spec describes **consumer-side substrate-decl preservation in
Go**. It mirrors the typed contract from
`shards/io/stagefreight.mirror` into Go type shapes and validation
functions, and wires them through the existing StageFreight runtime as
a thin adapter package. It is the twin of mirror PR #1 on the Go side
of the same wire. The IMPLEMENTATION of that adapter ships next tick;
this PR is the contract.

It is NOT:

1. The bytes-on-wire transport — that is forward-promised pending the
   Rust realisation layer on the mirror side. Until then the Go side
   exposes type shapes and validation predicates that downstream
   plumbing can wire up when the realisation lands.
2. A commitment to the `@io/stagefreight/json` projection format. The
   mirror substrate-decl is intentionally open (per spec §4.5); the Go
   side admits `narrative` as the first projection and leaves room for
   sibling species per the open-universe pattern.
3. A new StageFreight CLI subcommand surface. The wire protocol is
   substrate-decl-level; consumer-side it lives as a Go package
   importable by future lifecycle phases, not as `stagefreight mirror …`
   commands that ship in v0.1.
4. A modification of StageFreight's existing `@io`-shaped vocabulary
   (the cas store, the v2 outputs/results manifests, the lifecycle
   runtime). Those are StageFreight's own boundary disciplines and
   they intentionally stay independent of the mirror wire-protocol
   substrate. The adapter package is purely additive.

### 1.3 The multi-repo span (recognition #84) made operational

Per the @pack recognition that this work spans repos (StageFreight is
the consumer; mirror is the substrate-decl; the wire is the boundary),
the operational shape is:

```
mirror/main        @io/stagefreight family-root + /narrative species
                   docs/specs/stagefreight-wire-v0.1.md
                          │
                          │  (typed contract, OID-namespaced)
                          ▼
StageFreight/main  internal/stagefreightmirror/
                   Go type shapes + validation predicates
                   adapter package (no transport bytes yet)
                          │
                          │  (consumer-side substrate-decl)
                          ▼
forward-promise    bootstrap/src/stagefreight.rs (mirror PR-2)
                   bytes-on-wire emission + StageFreight server
                   wiring (this PR's package is the seam)
```

The cross-repo span is intentionally not collapsed: the mirror side
owns the substrate-decl ground truth (carriers, predicates, the
composed bilateral); the StageFreight side owns the runtime consumer
adapter and the bytes-on-wire termination point. The wire IS the
contract; both sides admit the same typed shape; neither side imports
the other (mirror is Rust target, StageFreight is Go binary; there is
no source-level dependency edge — only the wire-protocol contract).

### 1.4 Why now (alignment with the StageFreight runtime model)

StageFreight already has a runtime-spec discipline (`docs/runtime-spec.md`)
that treats the binary as a "lifecycle runtime that interprets
declarative repository intent, resolves runtime context from its
environment, dispatches to pluggable execution backends, and presents
structured, authoritative output." The mirror wire protocol fits this
exactly: a `freight_manifest` arriving at the wire surface IS
declarative repository intent (the crystal's substrate-decl content);
the consumer's job is to validate the typed contract and pass it to a
lifecycle backend.

Per StageFreight's runtime contract:

- The new consumer surface is a **LifecycleBackend** candidate (per
  `src/runtime/backend.go`). The four sub-predicates from mirror's
  composed bilateral map to the Validate phase; `address()` becomes
  a derivation function used during Plan; `freight()` becomes the
  request shape for Execute (when the bytes-on-wire transport lands).
- Per the runtime-spec's "Plan Determinism" rule (`docs/runtime-spec.md`
  §"Plan Determinism"), the wire address derivation MUST be a pure
  function of the input OID + projection-kind. The mirror substrate-
  decl's `address_well_formed(coord, oid)` predicate IS the
  determinism check; the Go side discharges it bit-for-bit.
- Per the runtime's "Backends must not write to stdout/stderr" rule,
  the adapter package returns typed values (validations, derived
  addresses, structured errors) and lets StageFreight's runtime layer
  own all output rendering.

This is why landing the consumer adapter NOW (before the Rust
realisation) is structurally clean: the runtime contract on the Go
side is stable; the type shapes are mechanically derivable from the
mirror substrate-decl; the bytes-on-wire layer plugs into a clean seam
when it arrives.

---

## §2. The contract surface (mirror → Go translation)

The mirror substrate-decl lists four typed carriers, three load-bearing
actions, four sub-predicates, and one composed bilateral. This section
maps each to its Go equivalent at the spec altitude. The next section
(§3) pins package layout; this section pins shape.

### 2.1 `spectral_coordinate` → Go type

Mirror substrate-decl:

```mirror
type spectral_coordinate = ref
```

(Forward-promised refinement at species-or-extension altitude: typed
record `{oid, namespace, projection_kind, version}` once the supporting
carriers land.)

Go translation:

```go
// SpectralCoordinate is the OID-namespaced wire address derived from a
// crystal's content-address via the reverse-DNS namespace + projection-
// kind + OID-short composition (per docs/specs/stagefreight-wire-v0.1.md
// §3.2). It is a named-string newtype — bare string is forbidden per
// mirror's [[feedback-no-bare-types]].
//
// Identity contract: byte-equality on the underlying string.
//
// Construction goes through DeriveSpectralCoordinate (§4); parsing
// goes through ParseSpectralCoordinate. Inline assembly is forbidden.
type SpectralCoordinate string
```

The Go side keeps the carrier as a newtype-over-string (rather than the
forward-promised record) to match the mirror substrate-decl's current
floor (`= ref`). Refinement to a typed record on the Go side is
forward-promised; it lands when the mirror side lifts.

Validation predicate `address_well_formed(coord, crystal_oid)`
translates to:

```go
// AddressWellFormed verifies the spectral_coordinate follows the
// derivation discipline given the originating crystal_oid: reverse-DNS
// namespace + projection-kind + OID-short composition, byte-deterministic
// from the OID. This is mirror's address_well_formed sub-predicate
// (Seam C2 closure) lifted to Go.
//
// Returns a verdict-shaped result (BOUNDED on success; DEFENSIVE
// with a structured reason on failure). The verdict type is named
// after the mirror substrate-decl convention to keep the contract
// surface legible across repos.
func AddressWellFormed(coord SpectralCoordinate, crystalOID OID) Verdict
```

### 2.2 `wire_surface` → Go type

Mirror substrate-decl:

```mirror
type wire_surface = ref
```

Go translation:

```go
// WireSurface is the typed transport endpoint where a crystal arrives
// on the wire. Parametric over projection format (narrative, json, …);
// the carrier itself is a named-string newtype; the format-specific
// payload is the underlying bytes.
//
// Identity contract: byte-equality on the underlying string.
type WireSurface string
```

The mirror substrate-decl deliberately leaves `wire_surface` as `ref`
because the wire vocabulary is parametric over projection format. The
Go translation honors this: `WireSurface` is a typed handle for "the
bytes were emitted to an endpoint the receiver can read." The concrete
shape (HTTP endpoint? gRPC stream? IPFS reference? local file path?)
is forward-promised to the realisation layer; the Go side admits the
opacity by carrying the handle through the type system without
prescribing its concrete protocol.

This is consistent with StageFreight's existing `cas.Store` interface
discipline (per `src/cas/cas.go`): a `Store.Transport()` capability is
declared but the concrete transport mechanism is plugged-in at
construction. The mirror wire surface follows the same pattern at a
higher altitude.

### 2.3 `freight_manifest` → Go struct

Mirror substrate-decl:

```mirror
type freight_manifest = {
  crystal_oid: ref,
  coord:       spectral_coordinate,
  projection:  ref,
  v:           verdict,
}
```

Go translation:

```go
// FreightManifest is the substrate-architectural record of a freight
// operation. Built by Freight() (§4); consumed by ProjectToNarrative
// (§5) and downstream wire-emission backends (forward-promised).
//
// Identity contract: byte-equality on the four-tuple. Determinism in
// the JSON serialization is required (per the mirror substrate-decl's
// byte-equality identity); the FreightManifestJSON canonical encoder
// (§3) handles this — standard encoding/json with field-order
// stability is sufficient because no maps appear in the schema.
type FreightManifest struct {
    CrystalOID  OID                 `json:"crystal_oid"`
    Coord       SpectralCoordinate  `json:"coord"`
    Projection  ProjectionRef       `json:"projection"`
    Verdict     Verdict             `json:"verdict"`
}
```

The `ProjectionRef` newtype is a Go-side specialization of the mirror
substrate-decl's `projection: ref` field. It names a substrate-decl'd
projection format (e.g. `@io/stagefreight/narrative`); parsing
discipline is in §3.

The `FreightManifest` struct deliberately mirrors the v2 outputs/results
manifest discipline (per `src/artifact/outputs.go`): strictly typed
fields, no maps in the schema, deterministic JSON serialization,
identity-as-content-address. The cross-system contract becomes legible
as "this is the StageFreight family of substrate-architectural records,
extended to the wire protocol."

### 2.4 The sub-predicates → Go validation functions

Per the Seam C4/C9 closure on the mirror side, the composed bilateral
`stagefreight_addressable(fm, p)` decomposes into four typed sub-
predicates. Each maps to a Go validation function returning a
structured verdict:

```go
// OIDResolves verifies the crystal_oid points at a settled crystal
// reachable in the configured @mirror/store backend. This is the
// forward-promised gate on task #268 (crystal substrate-decl); until
// that lands on the mirror side, the Go check is OID-shape-only
// (sha256:<hex>) and returns BOUNDED on shape match — the full
// reachability check fires when @mirror/store/crystal is realised.
//
// Honest hedge: until #268 lands and the @mirror/store wire-bridge is
// realised, OIDResolves cannot verify reachability — only that the
// OID is shape-compatible with the address derivation discipline.
// This is named explicitly in the verdict reason.
func OIDResolves(crystalOID OID, store StoreView) Verdict

// AddressWellFormed (see §2.1) — Seam C2 closure.
func AddressWellFormed(coord SpectralCoordinate, crystalOID OID) Verdict

// ProjectionIsSpecies verifies the projection ref names a substrate-
// decl'd @io/stagefreight/<species> projection format. The species
// registry (per §3) holds the admitted set; the Go side ships with
// "narrative" admitted by default; future species register via the
// open-universe pattern (Seam C3 LOOSE; structural openness).
func ProjectionIsSpecies(projection ProjectionRef, registry ProjectionRegistry) Verdict

// RoundTripHolds verifies the freight survives the round-trip under
// the named perturbation. The load-bearing wire-survival claim. Two
// concrete checks at v0.1:
//
//   (a) OID round-trip — emit the freight, parse it back, the OID
//       matches byte-for-byte. The structural floor.
//   (b) Address derivation round-trip — re-derive coord from
//       crystal_oid; byte-equality must hold.
//
// Forward-promised (gated on #268): full typed-field round-trip
// (section, derived_predicates, fracture_calendar, composition_graph).
func RoundTripHolds(fm FreightManifest, p Perturbation) Verdict
```

These are pure functions over the type carriers — no I/O at v0.1,
because the bytes-on-wire transport is forward-promised. They become
the Go side's contribution to the four-fold wire-survival discipline;
the realisation layer plugs the actual I/O in when the Rust side
lands.

### 2.5 The composed bilateral → Go middleware chain

Mirror substrate-decl:

```mirror
stagefreight_addressable(fm: freight_manifest, p: perturbation) -> verdict
requires oid_resolves(fm.crystal_oid)
requires address_well_formed(fm.coord, fm.crystal_oid)
requires projection_is_species(fm.projection)
requires round_trip_holds(fm, p)
{ \ }
```

Go translation:

```go
// StagefreightAddressable composes the four sub-predicates per the
// mirror substrate-decl's composition discipline (Seam C4/C9 closure).
// Short-circuits on the first DEFENSIVE verdict; collects the failing
// sub-claim's reason for upstream rendering.
//
// This is the boundary harness at the @io altitude — the alignment-
// as-boundary-mathematics (#57) discharge on the Go side. The
// substrate-pull-correct discipline says: the realisation gate fires
// here, before any bytes touch the wire.
//
// Five sub-predicates compose: the four wire-survival predicates
// (oid_resolves, address_well_formed, projection_is_species,
// round_trip_holds) plus the cross-family bilateral invariant_preserved
// from @magic. The fifth stage is BOUNDED-by-default at v0.1 (no-op
// stub; see magic.go) — the substantive discharge fires at Rust
// realisation when @magic contract types are wired through. The seam
// is present here so the cross-family bilateral has a Go-side landing
// surface; absence would silently drop the mirror substrate-decl's
// `requires invariant_preserved(c, promise)` clause.
func StagefreightAddressable(fm FreightManifest, p Perturbation, env Env) Verdict {
    if v := OIDResolves(fm.CrystalOID, env.Store); !v.OK() {
        return v.WithStage("oid_resolves")
    }
    if v := AddressWellFormed(fm.Coord, fm.CrystalOID); !v.OK() {
        return v.WithStage("address_well_formed")
    }
    if v := ProjectionIsSpecies(fm.Projection, env.Projections); !v.OK() {
        return v.WithStage("projection_is_species")
    }
    if v := RoundTripHolds(fm, p); !v.OK() {
        return v.WithStage("round_trip_holds")
    }
    if v := InvariantPreserved(env.Contract, env.Promise); !v.OK() {
        return v.WithStage("invariant_preserved")
    }
    return Bounded()
}
```

`Env` packages the runtime dependencies (store view, projection
registry, and the opaque @magic carriers — see §3.1 `env.go`) per
StageFreight's "all inputs explicit, no package vars" discipline
(`docs/architecture/boundaries.md`, "Service Function Rule"). The
Cobra adapter (when one lands) builds `Env` from flag/config inputs.

Note: `Env` carries the opaque `Contract` and `Promise` references
needed to discharge the fifth sub-predicate. They are accepted as
opaque carriers at v0.1 (see §3.1 `magic.go`); the cross-family
bilateral check returns BOUNDED by construction until the realisation
layer wires the @magic types through. This preserves the structural
seam — the `requires invariant_preserved(c, promise)` clause from the
mirror substrate-decl has a Go-side landing point at v0.1 rather than
being silently dropped.

### 2.6 The `freight()` action → Go constructor

Mirror substrate-decl:

```mirror
freight(crystal_oid: ref, coord: spectral_coordinate, projection: ref, c: magic_contract, promise: magic_invariant, p: perturbation) -> freight_manifest
requires address_well_formed(coord, crystal_oid)
requires invariant_preserved(c, promise)
{ \ }
```

Go translation:

```go
// FreightRequest packages the inputs to Freight per the service-function
// rule (boundaries.md). All inputs are explicit; no globals.
type FreightRequest struct {
    Ctx          context.Context
    CrystalOID   OID
    Coord        SpectralCoordinate
    Projection   ProjectionRef
    Contract     MagicContract     // forward-promised; opaque type at v0.1
    Promise      MagicInvariant    // forward-promised; opaque type at v0.1
    Perturbation Perturbation
    Env          Env
}

// Freight builds a FreightManifest after discharging BOTH requires
// clauses from the mirror substrate-decl, in composition order:
//
//   1. address_well_formed(coord, crystal_oid)
//   2. invariant_preserved(c, promise)        // cross-family @magic bilateral
//
// Composition order matches the mirror substrate-decl's `requires`
// stack. Short-circuits on the first DEFENSIVE verdict; the verdict's
// Stage names which sub-claim failed for upstream rendering.
//
// The @magic contract discharge is structurally present but BOUNDED-
// by-default at v0.1 — `InvariantPreserved` is a no-op stub (see §3.1
// magic.go) returning BOUNDED until the Rust realisation wires the
// @magic types through. This is consistent with mirror's "freight is
// the FIRST consumer of invariant_preserved" framing AND with the
// alignment-as-boundary-mathematics (#57) discharge at @io: the seam
// is present in the composition chain so the cross-family bilateral
// has a Go-side landing surface; the substantive verdict fires at
// realisation. Dropping the clause entirely (versus stubbing it) is
// what would have been the structural error — Seam C(b) closure.
func Freight(req FreightRequest) (FreightManifest, Verdict) {
    if v := AddressWellFormed(req.Coord, req.CrystalOID); !v.OK() {
        return FreightManifest{}, v.WithStage("address_well_formed")
    }
    if v := InvariantPreserved(req.Contract, req.Promise); !v.OK() {
        return FreightManifest{}, v.WithStage("invariant_preserved")
    }
    // … build manifest …
}
```

The Go-side `Freight` function takes opaque `MagicContract` +
`MagicInvariant` parameters AND threads `InvariantPreserved` as a
verdict step in the composition chain. The bilateral check returns
BOUNDED-by-default at v0.1 (no-op stub) — the substantive discharge
fires at Rust realisation when the @magic contract types are wired
through. This preserves the seam where mirror's cross-family
bilateral lands without committing the Go side to a particular
@magic implementation.

Note the deliberate Go-side opacity on `MagicContract` and
`MagicInvariant`. The mirror substrate-decl makes them load-bearing
through @magic family imports; on the Go side they are placeholder
opaque carriers (typed but unconstructible until realisation; see
§3.1 `magic.go`) until the cross-family bilateral discipline matters
at realisation time. This is an honest forward-promise, not a hidden
gap — it is named in §8 (what this PR does not do) and §11.x
(forward-promises).

### 2.7 The `address()` action → Go derivation function

```go
// DeriveSpectralCoordinate computes the wire address from a crystal's
// OID + projection-kind, per the address derivation discipline (§4).
// Pure function; deterministic; reverse-DNS namespace + projection-
// kind + OID-short composition.
//
// This is the construction. AddressWellFormed (§2.1) is the
// verification. Both share the derivation function as their oracle;
// AddressWellFormed re-derives and compares byte-for-byte.
func DeriveSpectralCoordinate(crystalOID OID, projection ProjectionRef) SpectralCoordinate
```

### 2.8 The narrative species → Go projection backend

Per `shards/io/stagefreight/narrative.mirror`, the first projection
species adds two carriers and three actions:

```mirror
type narrative_text       = ref
type narrative_projection = { fm, nt, verdict }

project_to_narrative(fm, p) -> narrative_projection
  requires stagefreight_addressable(fm, p)

narrative_grounded(np, p) -> verdict

finalize(np, p) -> wire_surface
  requires narrative_grounded(np, p)
```

Go translation:

```go
// NarrativeText is the rendered prose form of a crystal — typed
// reference; structured sections; readable + reconstructable.
//
// Identity contract: byte-equality on the underlying string.
type NarrativeText string

// NarrativeProjection records a narrative-projection operation.
type NarrativeProjection struct {
    Freight FreightManifest
    Text    NarrativeText
    Verdict Verdict
}

// ProjectToNarrative is the load-bearing FIRST consumer of
// StagefreightAddressable on the Go side. Renders the crystal's
// substrate-decl content (oid + section + derived_predicates +
// fracture_calendar + composition_graph) into structured prose.
//
// requires StagefreightAddressable — the freight must be addressable
// at the wire surface before projection is admissible.
//
// v0.1 hedge: the full crystal record is forward-promised on task
// #268. v0.1 projects the OID + projection-format + verdict into a
// minimal narrative template; field-by-field projection lands when
// the crystal substrate-decl arrives.
func ProjectToNarrative(req ProjectToNarrativeRequest) (NarrativeProjection, Verdict)

// NarrativeGrounded verifies the narrative_text preserves the
// crystal's substrate-decl structure under prose projection. Bounded
// → prose preserves crystal structure (Splinter-pole); Defensive →
// prose reads well but loses substrate-decl content (Narcissus-pole).
func NarrativeGrounded(np NarrativeProjection, p Perturbation) Verdict

// Finalize emits the projected narrative to the wire surface. Returns
// the typed WireSurface where the narrative arrives. requires
// NarrativeGrounded — finalization of an ungrounded narrative is
// foreclosed (Narcissus-pole prevention).
//
// v0.1: the bytes-on-wire emission is gated on the mirror realisation
// layer; v0.1 returns a placeholder WireSurface naming the intended
// emission target without performing the actual wire I/O. The seam is
// in place for the realisation layer to fill.
func Finalize(np NarrativeProjection, p Perturbation, env Env) (WireSurface, Verdict)
```

### 2.9 The `Verdict` type

The mirror substrate-decl uses `verdict` as a substrate-vocabulary
result type with two poles (BOUNDED → Splinter-pole; DEFENSIVE →
Narcissus-pole). The Go translation lifts this to a structured type:

```go
// VerdictKind is the substrate-pole of a verification.
type VerdictKind int

const (
    VerdictBounded   VerdictKind = iota // Splinter-pole — passes
    VerdictDefensive                    // Narcissus-pole — needs revision
)

// Verdict carries the outcome of a substrate-decl predicate check
// with structured failure information (stage, reason, sub-stage chain).
// Designed for composition: WithStage prepends; OK returns the
// pole-as-bool for short-circuit composition.
type Verdict struct {
    Kind   VerdictKind
    Stage  string   // which sub-predicate produced this verdict
    Reason string   // human-readable diagnosis (for upstream rendering)
    Chain  []string // accumulated sub-stages for composed bilaterals
}
```

This is the only type in the Go translation that does NOT map 1:1 to a
mirror substrate-decl carrier — it is a Go-runtime convenience
specialization of the substrate's `verdict` primitive. The mirror side
treats `verdict` as opaque (a substrate vocabulary word); the Go side
adds the rendering scaffolding StageFreight's runtime contract
requires (structured progress, machine-readable output envelopes).

---

## §3. The Go implementation skeleton

### 3.1 Package layout

The adapter package lands at `internal/stagefreightmirror/`, per
StageFreight's existing convention (cross-cutting integration glue
lives under `internal/`; cf `internal/docsgen/`). It is internal
because v0.1 does not commit to a public Go API; the package is for
StageFreight's own runtime layers to consume, not external Go
projects.

```
internal/stagefreightmirror/
├── doc.go              Package documentation; the mirror contract
│                       reference; the boundary discipline
├── coordinate.go       SpectralCoordinate type + DeriveSpectralCoordinate
│                       + ParseSpectralCoordinate + AddressWellFormed
├── coordinate_test.go  Round-trip identity tests; derivation
│                       determinism; reverse-DNS shape validation
├── manifest.go         FreightManifest struct + ProjectionRef +
│                       FreightManifest JSON encode/decode (deterministic)
├── manifest_test.go    Identity tests; byte-equality on the quadruple
├── freight.go          FreightRequest + Freight() + the requires
│                       chain composition
├── freight_test.go     Composition tests; short-circuit verification
├── addressable.go      StagefreightAddressable composed bilateral +
│                       the four sub-predicates
├── addressable_test.go Sub-predicate unit tests; composed verdict
│                       chain tests
├── narrative.go        NarrativeText + NarrativeProjection + the
│                       three narrative-species actions
├── narrative_test.go   Narrative-grounding tests; round-trip tests
├── verdict.go          Verdict type + VerdictKind + Bounded/Defensive
│                       constructors + WithStage composition helper
├── verdict_test.go     Composition tests
├── env.go              Env package struct + StoreView interface +
│                       ProjectionRegistry interface + opaque
│                       Contract/Promise refs for the cross-family
│                       @magic bilateral discharge
├── env_test.go
└── magic.go            MagicContract + MagicInvariant opaque
                        carriers (typed but unconstructible until
                        realisation) + InvariantPreserved no-op
                        stub (returns BOUNDED-by-default at v0.1)
```

#### 3.1.1 `magic.go` — opaque carriers + no-op stub

Per Seam C(i) closure, `MagicContract` and `MagicInvariant` are NOT
left as undeclared placeholders. They land as **opaque structs with
one unexported `_marker struct{}` field** — typed at the Go level but
unconstructible from outside `internal/stagefreightmirror` until the
Rust realisation provides the concrete shape and a constructor seam.

```go
// MagicContract is opaque at v0.1; structural marker preserves the
// mirror-side substrate-decl's `c: magic_contract` parameter. The
// _marker field is unexported and unconstructible from outside this
// package until Rust realisation lands and provides the concrete shape.
type MagicContract struct {
    _marker struct{}
}

// MagicInvariant is opaque at v0.1; structural marker preserves the
// mirror-side substrate-decl's `promise: magic_invariant` parameter.
// Same opacity discipline as MagicContract.
type MagicInvariant struct {
    _marker struct{}
}

// InvariantPreserved is the v0.1 no-op discharge stub for the cross-
// family bilateral. Returns BOUNDED-by-default; Rust realisation
// provides the substantive verdict when @magic contracts are wired
// through the wire.
func InvariantPreserved(c MagicContract, i MagicInvariant) Verdict {
    return Verdict{Stage: "invariant_preserved", Reason: "v0.1 stub", Chain: nil}
}
```

The opaque-struct choice (vs empty struct, interface, or `any`) is
Seam's recommendation: it preserves the substrate-decl type at the Go
level, prevents accidental construction by consumers, and gives the
realisation layer a single seam (the package-internal constructor) to
fill when the @magic types are wired through. The composition chains
in §2.5 and §2.6 reference this function; the seam is structural, not
merely documentary.

#### 3.1.2 `env.go` — Env package + interface forward-promises

The `Env` struct packages all runtime dependencies for the
composition chains. v0.1 shape:

```go
type Env struct {
    Store        StoreView
    Projections  ProjectionRegistry
    Contract     MagicContract     // opaque; threads to InvariantPreserved
    Promise      MagicInvariant    // opaque; threads to InvariantPreserved
}

type StoreView interface {
    // realisation-layer surface; see forward-promise note below
}

type ProjectionRegistry interface {
    // realisation-layer surface; see forward-promise note below
}
```

**Forward-promise note (Seam C(f) closure):** `StoreView` and
`ProjectionRegistry` interfaces are Go-side conveniences. They MUST
map to substrate concepts in the realisation layer: `StoreView` →
`@mirror/store` interface; `ProjectionRegistry` →
`@io/stagefreight/<species>` registry. Forward-promised to the
realisation PR; this v0.1 spec uses `interface{}`-anchored shapes
that will gain concrete methods when the Rust realisation pins the
substrate-side method surface. The interfaces are intentionally
unmethoded at v0.1 — they exist as Go-side type carriers for the
composition signatures, not as a commitment to a particular set of
operations on the Go side ahead of substrate ratification.

### 3.2 Boundary discipline (per StageFreight's `docs/architecture/boundaries.md`)

`internal/stagefreightmirror` MUST follow the existing boundary rules:

- **Permitted internal imports:** none at v0.1. The package is a
  foundation layer (like `src/artifact` or `src/config`) — it carries
  type carriers and pure validation, no orchestration or I/O.
- **Forbidden internal imports:** `cli/cmd` (the dependency-graph leaf;
  no Cobra wiring lives here), `build/*` (the wire protocol is not a
  build-system concern at v0.1), `postbuild`, `registry`, `forge`,
  `release`.
- **Permitted external imports:** stdlib only at v0.1. The bytes-on-
  wire transport is forward-promised; until it lands, the package
  needs no transport libraries.

When the realisation layer arrives, a sibling package
`internal/stagefreightwire/` may grow to host the bytes-on-wire layer,
importing `internal/stagefreightmirror` for the type carriers. That
package would be permitted `net/http` or `google.golang.org/grpc`
imports per the realisation discipline. v0.1 does not pre-commit to
either.

### 3.3 Test discipline (Go ↔ mirror RED stubs)

The mirror side carries RED test stubs at:

```
bootstrap/tests/crystal_substrate.rs           (line 9; org.stagefreight.plan.spectral_coordinate)
bootstrap/tests/kintsugi_out_substrate_ref.rs  (lines 23-24; @io/stagefreight/narrative)
```

These stubs flip GREEN only when the Rust realisation lands; they are
NOT this PR's responsibility. But the Go side ships parallel test
coverage at v0.1 — these are GREEN-from-start in the StageFreight
repository:

```
coordinate_test.go::TestDeriveSpectralCoordinate_RoundTrip
coordinate_test.go::TestDeriveSpectralCoordinate_Deterministic
coordinate_test.go::TestParseSpectralCoordinate_ReverseDNSShape
coordinate_test.go::TestAddressWellFormed_BoundedOnDerivedCoord
coordinate_test.go::TestAddressWellFormed_DefensiveOnTampered
manifest_test.go::TestFreightManifest_ByteEquality
manifest_test.go::TestFreightManifest_JSONDeterminism
freight_test.go::TestFreight_ComposedRequires
freight_test.go::TestFreight_FailsOnUnderiveableCoord
addressable_test.go::TestStagefreightAddressable_ComposedBilateral
addressable_test.go::TestStagefreightAddressable_ShortCircuit
narrative_test.go::TestProjectToNarrative_RequiresAddressable
narrative_test.go::TestNarrativeGrounded_BoundedOnPreservation
narrative_test.go::TestFinalize_RequiresGrounded
```

These tests verify the type-shape contract is faithful to the mirror
substrate-decl. They do NOT verify bytes-on-wire round-trip (that
requires the realisation layer on both sides); they verify the
Go-side derivation discipline and the composed bilateral chain.

When the mirror realisation lands, a cross-repo integration test
fixture (forward-promised; lives in mirror's bootstrap tests, not
StageFreight's repo) will exercise the round-trip end-to-end.
StageFreight's local tests guarantee its side of the contract is
correct in isolation.

---

## §4. The address derivation in Go

The address derivation discipline is the load-bearing structural rule
that bridges OID → wire address. Per mirror's spec §3.2 and Seam C2
closure, the rules are:

1. Reverse-DNS namespace (e.g. `org.stagefreight.plan`)
2. Projection-kind component (e.g. `narrative`)
3. OID-short component (truncated content-address)
4. Byte-deterministic composition

### 4.1 Reverse-DNS namespacing

Per the mirror substrate-decl test stub (`bootstrap/tests/crystal_substrate.rs:9`),
the canonical namespace is `org.stagefreight.plan`. This is the
StageFreight repository's organizational namespace at the wire surface.

The pre-AI prior art is well-grounded:

- **Java packages** (Sun, 1995) introduced reverse-DNS as a
  package-organizational discipline. The substrate ancestor of
  org-level namespacing.
- **Apple bundle identifiers** (early 2000s) extended reverse-DNS to
  application identity. `com.apple.MobileSafari` is the cultural
  ancestor of `org.stagefreight.plan.<oid>`.
- **OSGi bundle symbolic names** generalized the discipline to
  modular Java systems.
- **DBus interface names** apply it to interprocess messaging.

The discipline is older and broader than any single ecosystem, which
matches the mirror substrate-decl's intent — naming the universal
address derivation, not a StageFreight-specific convention.

### 4.2 The derivation function

```go
// DeriveSpectralCoordinate computes the wire address per the
// derivation discipline. Pure; deterministic; total over valid
// (oid, projection) pairs.
//
// Format: <reverse-dns-namespace>.<projection-kind>.<oid-short>
//
// Example: crystal_oid="sha256:a1b2c3..." projection="narrative"
//          → "org.stagefreight.plan.narrative.a1b2c3"
//
// The OID-short component is the first 12 hex chars of the OID's
// hash portion (collision-resistant within a single project's
// address space; full OID for global uniqueness recoverable from
// the crystal store).
func DeriveSpectralCoordinate(crystalOID OID, projection ProjectionRef) SpectralCoordinate {
    return SpectralCoordinate(fmt.Sprintf("%s.%s.%s",
        addressNamespace,                  // "org.stagefreight.plan"
        projection.Kind(),                  // "narrative"
        crystalOID.Short(),                 // first 12 hex chars
    ))
}
```

The `addressNamespace` constant is a Go package-level value; it is
the StageFreight repository's commitment to its place in the global
wire-address namespace. Changing it is a versioned API break and
requires coordination with the mirror substrate-decl.

### 4.3 Round-trip identity

Per mirror spec §5.1 and the Seam-closed C2 + C4/C9 wire-survival
discipline, the wire address MUST round-trip the OID byte-for-byte:

```go
// ParseSpectralCoordinate extracts the (oid-short, projection-kind)
// pair from a SpectralCoordinate. The full OID is recovered by
// lookup in the store keyed by oid-short.
//
// Returns Defensive if the coord doesn't follow the derivation shape.
func ParseSpectralCoordinate(coord SpectralCoordinate) (oidShort string, kind string, v Verdict)

// AddressWellFormed verifies coord matches the derivation discipline
// given the originating crystalOID. The check:
//
//   1. Parse coord → (oidShort, kind)
//   2. Re-derive expectedCoord from (crystalOID, projection) using
//      the same projection-kind
//   3. Byte-equality: coord == expectedCoord
//
// This is the Seam C2 closure on the Go side: the address rules live
// in the predicate, not in prose only.
func AddressWellFormed(coord SpectralCoordinate, crystalOID OID) Verdict
```

Round-trip identity is the floor of the wire-survival discipline. The
v0.1 GREEN tests in `coordinate_test.go` exercise this directly.

### 4.4 What the v0.1 derivation does NOT do

- It does NOT validate the OID is reachable in the configured store
  (that's `OIDResolves`, gated on task #268).
- It does NOT validate the projection-kind names a registered species
  (that's `ProjectionIsSpecies`, with an open-universe registry).
- It does NOT commit to the OID-short truncation length being
  collision-free at global scale. The 12-hex-char truncation is the
  v0.1 commitment; longer truncations (or full OID) are forward-
  promised refinements gated on production use.

---

## §5. The `@io/stagefreight/narrative` projection in Go

### 5.1 The `NarrativeText` type and v0.1 hedge

```go
// NarrativeText is the rendered prose form of a crystal — a typed
// reference per [[feedback-no-bare-types]]; the underlying string is
// the prose payload.
type NarrativeText string
```

v0.1 ships a minimal prose template:

```text
StageFreight crystal projection — narrative form

Crystal OID:     sha256:<full-oid>
Wire coordinate: <spectral-coordinate>
Projection:      @io/stagefreight/narrative
Verdict:         BOUNDED | DEFENSIVE (with reason)

(crystal substrate-decl fields land when task #268 closes:
  section, derived_predicates, fracture_calendar, composition_graph)
```

This is an honest hedge: the v0.1 prose carries the wire-protocol
fields (OID, coord, projection, verdict) byte-deterministically; the
crystal's typed-record fields (section, etc.) are forward-promised.

### 5.2 The `ProjectToNarrative` request type

```go
type ProjectToNarrativeRequest struct {
    Ctx          context.Context
    Freight      FreightManifest
    Perturbation Perturbation
    Env          Env
}

func ProjectToNarrative(req ProjectToNarrativeRequest) (NarrativeProjection, Verdict) {
    // requires StagefreightAddressable
    if v := StagefreightAddressable(req.Freight, req.Perturbation, req.Env); !v.OK() {
        return NarrativeProjection{}, v.WithStage("project_to_narrative")
    }
    nt := renderNarrative(req.Freight)
    return NarrativeProjection{
        Freight: req.Freight,
        Text:    nt,
        Verdict: Bounded(),
    }, Bounded()
}
```

The composition order is locked: address-well-formedness then
projection. This matches mirror's substrate-decl `requires` chain
exactly.

### 5.3 The `Finalize` action

```go
type FinalizeRequest struct {
    Ctx          context.Context
    Projection   NarrativeProjection
    Perturbation Perturbation
    Env          Env
}

func Finalize(req FinalizeRequest) (WireSurface, Verdict) {
    // requires NarrativeGrounded
    if v := NarrativeGrounded(req.Projection, req.Perturbation); !v.OK() {
        return "", v.WithStage("finalize")
    }
    // v0.1: return a placeholder WireSurface naming the intended
    // emission target. The bytes-on-wire emission seam is in place
    // for the realisation layer (mirror PR-2 + StageFreight follow-up)
    // to fill.
    surface := WireSurface(fmt.Sprintf("stagefreight://wire/%s",
        req.Projection.Freight.Coord))
    return surface, Bounded()
}
```

The `stagefreight://wire/` placeholder is the v0.1 commitment — a
typed handle for "the bytes would land here." The realisation layer
fills in the concrete transport (HTTP POST? gRPC server-streaming?
WebSocket? local file?) when it arrives. The seam is honest about
what it does not yet do.

---

## §6. CI integration

### 6.1 GitLab CI shape

StageFreight uses GitLab CI per `.gitlab-ci.yml` (with GitHub
synchronization via `forges` config in `.stagefreight.yml`). The
pipeline has five stages: `audition`, `perform`, `review`, `publish`,
`narrate`. Per `docs/architecture/transport-rollout.md`, the binary
dogfoods itself.

The mirror integration spec PR does NOT add new CI stages. It adds
test coverage that runs within the existing `audition` and `perform`
stages:

- **audition:** `go test ./internal/stagefreightmirror/...` runs as
  part of the existing lint/test pass. The 15 GREEN tests (§3.3)
  must all pass for the audition to admit the change.
- **perform:** the new package compiles into the StageFreight binary;
  no behavior change because nothing yet calls it. The binary's
  release artifacts unchanged.
- **review:** no security-scan implication at v0.1 (pure Go, no new
  dependencies, no transport surface).
- **publish:** unchanged.
- **narrate:** the narrator regenerates `docs/reference/CLI.md` and
  badges as part of the normal docs-refresh cycle. No new CLI
  surface in v0.1 means narration is no-op for the mirror package.

### 6.2 Mapping to mirror's `mirror kintsugi --ci` pattern

On the mirror side, the kintsugi loop validates substrate consistency
in CI via `mirror kintsugi --ci`. StageFreight's equivalent check —
verifying the consumer side's type shapes match the substrate-decl —
is forward-promised at v0.2:

```bash
# Forward-promised CI check (not in v0.1):
stagefreight mirror validate --shard shards/io/stagefreight.mirror
```

This would re-read the mirror substrate-decl and verify the Go side's
type shapes match (carriers, predicate signatures, composed bilateral
chain). v0.1 does NOT ship this command; the verification lives in
human review of cross-repo diffs until automated.

### 6.3 What v0.1 does NOT change in `.gitlab-ci.yml`

Per the CLAUDE.md "STAY ON TASK" rule, this PR does not touch
`.gitlab-ci.yml` or `.stagefreight.yml`. The test coverage runs in
existing pipeline shape.

Specifically the v0.1 PR does NOT:

- Add a new CI stage
- Modify existing stage scripts
- Touch the `targets:`, `builds:`, or `narrator:` blocks of
  `.stagefreight.yml`
- Require new GitLab CI variables or OIDC scopes
- Modify the Docker image build

The substrate-decl preservation discipline (mirror side) and the
consumer-side substrate-decl preservation (this side) are
intentionally additive-only.

---

## §7. Falsification criteria

Per mirror spec §8, the substrate-decl carries falsification claims
the realisation layer must discharge. The Go side's claims at v0.1
are narrower (no transport yet) but the discipline is identical.

### 7.1 Round-trip identity

**Claim:** `DeriveSpectralCoordinate` round-trips the OID byte-for-
byte through `ParseSpectralCoordinate` + the store-side OID lookup.

**Falsification:** any oid where derivation + parsing + lookup does
not recover the original OID is a wire-survival failure.

**Discharge:** `TestDeriveSpectralCoordinate_RoundTrip` in
`coordinate_test.go`. Property-based test over 1000 random sha256
OIDs.

### 7.2 Cross-projection consistency (Splinter-pole vs Narcissus-pole)

**Claim (per mirror spec §8.4):** the OID is projection-agnostic.
Given crystals A and B with OID(A) == OID(B), the wire address
derived for `narrative` projection of A and `narrative` projection
of B is byte-equal.

**Falsification:** a derivation that varies under irrelevant inputs
(timestamps, user identity, build context).

**Discharge (v0.1):** `TestDeriveSpectralCoordinate_Deterministic`
exercises this directly. The full cross-projection claim (narrative
vs json) is gated on a second projection species landing on the
mirror side; v0.1 ships the consistency-within-narrative property.

**Honest hedge:** the full cross-projection falsification fires when
`@io/stagefreight/json` (or any sibling species) lands on the mirror
side. Tracked as a forward-promise in §9.

### 7.3 Wire-survival under perturbation

**Claim:** the four sub-predicates short-circuit correctly under the
named perturbation; an addressable freight at perturbation p1 remains
addressable at any "weakening" perturbation p2 (monotonicity).

**Falsification:** a perturbation under which `StagefreightAddressable`
returns BOUNDED, but a strictly-stronger perturbation returns
DEFENSIVE — that's the substrate-decl claim breaking under composition.

**Discharge:** `TestStagefreightAddressable_ComposedBilateral` tests
the monotonicity property across a fixed family of perturbations.

### 7.4 Narrative-grounding preservation

**Claim:** `ProjectToNarrative` produces a `NarrativeText` from which
the original `FreightManifest`'s OID + Coord + Projection are
recoverable byte-for-byte (Splinter-pole). A `NarrativeText` that
reads well but loses substrate-decl fields is Narcissus-pole and
must be DEFENSIVE.

**Falsification:** any `FreightManifest` fm such that
`ProjectToNarrative(fm).Text` does not encode `fm.CrystalOID`,
`fm.Coord`, `fm.Projection` recoverable structurally.

**Discharge:** `TestNarrativeGrounded_BoundedOnPreservation` and
`TestProjectToNarrative_RoundTrip`.

### 7.5 What v0.1 cannot yet falsify

- The bytes-on-wire round-trip claim (no transport yet).
- The crystal's typed-record field-by-field round-trip (gated on
  task #268).
- The receiver-side reconstruction discipline (the receiver doesn't
  exist on the Go side at v0.1).

These are forward-promises tracked in §9.

---

## §8. What this PR does NOT do

This PR is consumer-side substrate-decl preservation. The substrate-
pull-honest framing requires naming what it does NOT do.

### 8.1 Does NOT depend on `@mirror/store/crystal` (task #268)

The Go-side type shapes treat the crystal as an OID-typed reference,
per the mirror substrate-decl's deliberate decoupling (spec §1.3).
The `FreightManifest.CrystalOID` field is `OID` (a typed sha256
reference); the full crystal record is forward-promised at v0.2 when
#268 closes on the mirror side.

This means v0.1 ships an OID-floor wire-protocol consumer. The
crystal's substrate-decl fields (section, derived_predicates,
fracture_calendar, composition_graph) do NOT appear in
`FreightManifest` because they are forward-promised on the mirror
side. The Go side admits the same forward-promise.

### 8.2 Does NOT implement the Rust realisation side

`bootstrap/src/stagefreight.rs` (mirror) is the realisation layer
for the wire-protocol bytes. It is forward-promised on the mirror
side pending:

1. Phase 1 boot-grammar zero holonomy
2. Phase 4b @kintsugi/tick discharge
3. Direct Rust hand-write OR Phase 4 emitter codegen

This PR does NOT touch that side. It opens the seam on the Go side
that the realisation layer will plug into.

### 8.3 Does NOT commit StageFreight's existing structure to changes

The mirror substrate-decl might revise carrier shapes or predicate
signatures at v0.2 (refinement of `spectral_coordinate` from `ref` to
a typed record; addition of new sub-predicates; etc.). The Go adapter
package is designed to absorb such revisions WITHOUT requiring
changes to StageFreight's existing runtime, cas, artifact, or
lifecycle code.

This is the deliberate "thin adapter shape" framing. The mirror
contract has a long forward-promise tail (multi-tick realisation
cascade). The Go side stays narrow and additive until each tick
lands.

### 8.4 Does NOT add a `stagefreight mirror …` CLI surface

The mirror integration at v0.1 lives as a Go package (`internal/
stagefreightmirror`). It is consumed in the future by lifecycle
backends, not by direct CLI invocation. A `stagefreight mirror
validate` command is forward-promised at v0.2 (per §6.2) when the
CI verification discipline matures.

### 8.5 Does NOT prescribe additional projection species

The mirror side admits an open universe of projection species
(narrative, json, yaml, brainfuck-compressed, …). v0.1 ships
`narrative` only. Sibling species register through the open-universe
pattern when they land on the mirror side; the Go `ProjectionRegistry`
is designed to admit them additively.

### 8.6 Does NOT modify the existing `@io`-shaped vocabulary of StageFreight

StageFreight already has rich `@io`-shaped vocabulary (cas store,
v2 outputs/results manifests, lifecycle runtime, perform→publish
transport). This PR does NOT touch any of it. The mirror wire-
protocol integration is a separate boundary species (per
StageFreight-as-substrate-pull-prior-art: the existing boundary
disciplines are independent of the mirror wire protocol; both can
coexist).

---

## §9. Forward-promises after this spec

Ordered by likely cascade fire:

### 9.1 PR draft branch + diff sketch (this tick)

- Branch: `mara/stagefreight-mirror-integration-spec-v0.1`
- Commit: `📝 Mara [substrate-pull:realize] docs/architecture/
  mirror-integration-spec-v0.1.md — canonical spec for @io/stagefreight
  contract implementation`
- Status: this spec, then Reed reviews before push.

### 9.2 Implementation tick (next)

The 14-file Go package skeleton (per §3.1):

```
internal/stagefreightmirror/
  doc.go coordinate.go coordinate_test.go
  manifest.go manifest_test.go
  freight.go freight_test.go
  addressable.go addressable_test.go
  narrative.go narrative_test.go
  verdict.go verdict_test.go
  env.go env_test.go magic.go
```

GREEN-from-start; 15+ tests passing; zero StageFreight runtime impact.

### 9.3 Integration test fixture (forward-promised after realisation)

A cross-repo test fixture that exercises the wire-protocol round-trip
end-to-end: mirror's Rust realisation emits bytes; StageFreight's Go
adapter receives them; the round-trip identity holds. This lives in
mirror's `bootstrap/tests/`, not StageFreight's repo (the Rust side
owns the round-trip discharge); StageFreight ships a test client
binary the fixture invokes.

### 9.4 StageFreight v0.x release tag with the mirror package

When the realisation layer lands and the cross-repo integration test
goes GREEN, StageFreight tags a v0.x release that ships the
consumer-side wire-protocol support. The tag opens StageFreight's
public commitment to the wire contract.

### 9.5 Mirror PR-2 (Rust realisation) closes the round-trip

The mirror realisation PR lands `bootstrap/src/stagefreight.rs` and
flips the two RED test stubs (`crystal_substrate.rs`,
`kintsugi_out_substrate_ref.rs`) to GREEN. At that point the cross-
repo cascade is operationally closed: both sides honor the typed
contract; the wire protocol is end-to-end discharged.

Per mirror's release-notes forward-promise (`docs/releases/
stagefreight-v0.1.0-substrate-decl.md`):

> Total forward-promise: ~13-22 additional ticks (per the path-to-v1
> research grounding).

The StageFreight side adds a comparable tail (the Go package, the
test client, the release tag). The cascade total is the sum.

### 9.6 The `@io/stagefreight/json` projection species

Once the mirror side admits a second projection species, the
StageFreight side adds:

```
internal/stagefreightmirror/json.go
internal/stagefreightmirror/json_test.go
```

This is when the cross-projection consistency falsification (§7.2)
becomes meaningfully testable.

---

## §10. Pre-AI prior art

### 10.1 Go service patterns

StageFreight's adapter package follows established Go-server patterns:

- **`net/http` middleware composition** (Go stdlib) — the chain-of-
  validators pattern. `StagefreightAddressable`'s short-circuit
  composition is structurally identical to `http.Handler` chains.
- **request-shaped functions** (Go community convention) — explicit
  request structs (cf. `aws-sdk-go-v2`'s `XxxInput` types) avoid the
  positional-argument-explosion antipattern.
- **`context.Context` as first parameter** (Go 1.7+ idiom) — the
  cancellation discipline aligns with StageFreight's existing
  `RunXxxRequest{Ctx context.Context, ...}` pattern (per
  `docs/architecture/boundaries.md`'s "Service Function Rule").

### 10.2 Wire-protocol implementations

- **gRPC** (Google, 2015) — typed wire contracts via protobuf;
  service-method shape. The mirror substrate-decl's `freight()` /
  `transit()` action discipline echoes gRPC's request/response shape
  but at a higher altitude (the wire is opaque; the substrate-decl
  is the schema).
- **REST + JSON Schema** (Fielding 2000; JSON Schema 2009) — typed
  contracts over a stateless transport. The discipline ancestor of
  "the typed contract is one layer; the transport is another."
- **IPFS Bitswap** (Benet 2014) — content-addressed wire protocol;
  the spectral_coordinate naming echoes IPFS's CID discipline. The
  mirror substrate-decl cites this directly (`source @arxiv/
  distributed/benet-2014`).
- **Cap'n Proto** (Varda 2013) — zero-copy typed wire protocol;
  cited as `source @arxiv/protocols/varda-2013` in the family-root.
  The structure-preserving transit discipline ancestor.

### 10.3 Reverse-DNS namespacing in Go

- **Go module paths** (Go 1.11+) are reverse-DNS by convention
  (`github.com/PrPlanIT/StageFreight`). The Go ecosystem already
  speaks this dialect natively.
- **Apple bundle identifiers** (`com.apple.MobileSafari`) — the
  cultural-practice ancestor of `org.stagefreight.plan.*`.
- **Java package naming** (Sun, 1995) — the original reverse-DNS
  organizational discipline; `com.sun.*`, `org.apache.*`.
- **DBus interface names** — IPC dialect of the same discipline.

### 10.4 The 2026-06-16 `stage_play` recognition cascade

The mirror substrate-decl's `stage_play` cascade (Story → Play →
Narrative) is the immediate intellectual ancestor of StageFreight's
wire protocol. The crystal IS the play's settled artifact; the
narrative IS the prose form ready for transmission;
`@io/stagefreight` names the wire surface that ships it. Cited as
substrate ancestry in the mirror family-root.

### 10.5 StageFreight's own substrate prior art

StageFreight already exhibits several substrate-pull patterns that
this integration honors:

- **Typed identity, not stringly-typed** (per `src/artifact/outputs.go`'s
  `Digest`, `ArtifactID` discipline). The mirror integration extends
  this to `SpectralCoordinate`, `OID`, `ProjectionRef`, etc.
- **Verify-on-read** (per `cas.VerifyLayoutAt`). The mirror
  integration extends this discipline to wire-protocol validation —
  no handle is trusted as bare claim; every freight re-validates.
- **Workspace-scoped lifecycle** (per `docs/architecture/content-
  store-lifecycle.md`). The mirror integration respects this: the
  adapter package has no global state; all environment-dependent
  operations take `Env` explicitly.
- **Phase-determinism** (per `docs/architecture/persistence-
  identity.md`). The mirror integration's `DeriveSpectralCoordinate`
  is a pure function; the determinism discipline matches the
  persistence-handle algebra's identity invariant.

The cross-repo cascade is recognizing the same substrate-pull
discipline on both sides of the wire.

---

## §11. The PR shape

### 11.1 Title

```
docs(architecture): @io/stagefreight contract implementation spec v0.1 (consumer-side substrate-decl)
```

### 11.2 Branch

```
mara/stagefreight-mirror-integration-spec-v0.1
```

### 11.3 Diff size estimate

This PR (the spec only): ~900 lines (this file).

Forward-promised implementation PR (the Go package, §9.2): ~1,200-
1,800 lines across 14 files (12 source + tests). Roughly:

```
coordinate.go               ~100 lines
coordinate_test.go          ~150 lines
manifest.go                  ~80 lines
manifest_test.go            ~100 lines
freight.go                   ~80 lines
freight_test.go             ~120 lines
addressable.go              ~120 lines
addressable_test.go         ~180 lines
narrative.go                ~150 lines
narrative_test.go           ~200 lines
verdict.go                   ~80 lines
verdict_test.go             ~100 lines
env.go                       ~60 lines
magic.go                     ~40 lines
doc.go                       ~50 lines
```

### 11.4 Forward-promises in the PR description (template)

```markdown
## What this PR does

Lands the canonical spec for the consumer-side `@io/stagefreight`
contract implementation in the StageFreight Go repository. Cross-repo
cascade tick (continues from mirror PR #1, the substrate-decl
preservation tick that merged 2026-06-22).

## What this PR does NOT do

- Implement the Go package (forward-promised at next tick)
- Touch existing StageFreight runtime / cas / lifecycle code
- Add new CLI surface or CI stages
- Modify `.stagefreight.yml` or `.gitlab-ci.yml`
- Depend on mirror's Rust realisation layer (forward-promised)

## Forward-promises

1. Go package skeleton (`internal/stagefreightmirror/`, ~14 files)
2. GREEN-from-start test coverage (15+ tests)
3. Cross-repo integration fixture (after mirror realisation lands)
4. StageFreight v0.x release tag with wire-protocol support
5. `@io/stagefreight/json` projection species (after mirror sibling)
6. Substantive cross-family @magic bilateral discharge (gated on
   Rust realisation + Pack ratification of consumer-side deferral
   policy; see §11.6.1)
7. Concrete `StoreView` / `ProjectionRegistry` interface methods
   (gated on realisation-layer pinning of `@mirror/store` +
   `@io/stagefreight/<species>` registry surfaces; see §3.1.2)

## Substrate-pull discipline

- Type carriers (newtypes; no bare strings; no bare `[]byte`)
- Composed bilateral chain (short-circuiting; structured verdicts)
- Deterministic derivation (pure functions; round-trip identity)
- Honest hedges where realisation forward-promises haven't fired
- Cross-repo span made operational (mirror substrate-decl + Go
  adapter; the wire is the boundary; neither side imports the other)

## Pre-AI prior art (per §10)

Go service patterns; gRPC; REST+JSON Schema; IPFS Bitswap; Cap'n
Proto; reverse-DNS namespacing (Java/Apple/DBus); StageFreight's own
substrate (verify-on-read, typed identity, workspace-scoped
lifecycle, phase-determinism).
```

### 11.5 Commit message (for the spec landing)

```
📝 Mara [substrate-pull:realize] docs/architecture/mirror-integration-spec-v0.1.md
— canonical spec for @io/stagefreight contract implementation
```

Per the Pack convention; substrate-pull-correct framing; spec-writer-
peer attribution.

### 11.6 What lands now vs what lands later

```
NOW (this commit):
  docs/architecture/mirror-integration-spec-v0.1.md

NEXT TICK (forward-promised; separate commit/PR):
  internal/stagefreightmirror/*.go        (Go package skeleton)
  internal/stagefreightmirror/*_test.go   (GREEN tests)

LATER (forward-promised; gated on mirror realisation):
  bootstrap-side cross-repo integration test fixture
  StageFreight v0.x release tag
  internal/stagefreightmirror/json.go     (sibling species)
```

### 11.6.1 The cross-family @magic bilateral deferral (forward-promise, not ratification)

The Go-side `InvariantPreserved` stub (§3.1 `magic.go`) returns
BOUNDED by construction at v0.1. This is a **FORWARD-PROMISE, not a
RATIFIED Pack decision**. The substrate-pull-correct reading is:

- The cross-family bilateral `invariant_preserved(c, promise)` is
  declared in the mirror substrate-decl as a `freight()` requires
  clause. Dropping it Go-side would silently break the alignment-as-
  boundary-mathematics (#57) discharge.
- At v0.1 the Go side preserves the seam (composition step present
  in §2.5 + §2.6; opaque carriers declared in §3.1) but defers
  substantive discharge to the Rust realisation layer, which has the
  @magic family imports and can validate the contract structurally.
- **If the Pack later ratifies full deferral** of the cross-family
  bilateral to the realisation layer (i.e. agrees the Go side need
  not carry the carriers or the composition step), the no-op stub IS
  the discharge — the v0.1 shape collapses cleanly into the ratified
  shape with no spec rewrite. If the Pack instead ratifies that the
  Go side must carry substantive validation, the realisation layer
  fills the stub body and the composition chain already routes through
  it. Both ratification paths are honored by the v0.1 structure.
- The hedge is named here, not elided. The substrate's discipline is
  that forward-promises are explicit; this one is.

### 11.7 Honest framing

This PR is **spec preservation**. It does NOT ship the implementation.
Per the Pack-as-orchestra discipline (mirror release notes),
substrate-decl-honest framing prevents the aspirational endpoint
collapse:

> Frame the PR as 'StageFreight substrate-decl v0.1 (preservation tick;
> realisation forward-promised)' and the endpoint is honest. Frame it
> as 'StageFreight v0.1 endpoint' and it's aspirational.

The StageFreight side adopts the same discipline. This PR is the
consumer-side preservation tick. The realisation forward-promise tail
is multi-tick; honesty about that is part of the substrate-pull
discipline.

---

## §12. Acknowledgments

- **Cascade architecture session** (2026-06-23): §§14–§19 broadening
  developed same-day via Pack-discipline cascade — @cascade
  substrate-decl tick (Reed, `shards/cascade.mirror`, recognition #95
  candidate); typed-alternatives survey (Mara, `docs/research/
  2026-06-23-typed-alternatives-cascade-survey.md`, 10 stacks); this
  spec's multi-language broadening + math formalization (Mara); Seam
  adversarial review of the broadened spec (BOUNDED-with-revisions;
  this consolidation tick).
- **Mirror substrate-decl track** (Reed, tick 66-69): family-root +
  narrative species + Seam C2/C4/C9/C8/C10 closures.
- **Canonical spec** (Mara, tick 68): 1535-line spec providing the
  substrate-ground for this consumer-side spec.
- **Seam adversarial review** (Seam, tick 69): 3 TIGHT findings closed
  on the mirror side; the DEFENSIVE → BOUNDED arc that makes the
  contract substrate-decl-correct.
- **The 2026-06-16 `stage_play` recognition cascade** (Alex): the
  intellectual ancestor that named what the wire surface IS.
- **StageFreight's existing substrate prior art**: the verify-on-read
  discipline, the workspace-scoped lifecycle, the persistence-identity
  algebra — all converged with the mirror substrate-decl at the
  consumer-side adapter altitude.

The substrate is honest about what it ships and what it doesn't. This
spec ships what the consumer-side substrate-decl preservation IS; the
implementation discharges next.

*(Sign-off consolidated to the doc footer; see end of file.)*

---

## §13. Solves your current MVP work

The author's MVP-in-flight already names two structural pieces the
mirror substrate-decl carries as first-class primitives. This section
maps that overlap concretely; the framing collapse the spec wants to
defuse is "mirror is one more integration" — the substrate-pull-correct
reading is "mirror IS the typed shape of work already happening."

### 13.1 Author's caching → content-addressed `oid` keying

The MVP caching work derives cache keys from build inputs to skip
recomputation on subsequent builds. The mirror substrate already
discharges this discipline at the `splinter` carrier altitude:
`splinter.content: oid` is the universal content-addressed atom
(`shards/glass.mirror` line 222). Two splinters with byte-equal
content share an oid by construction; cache identity IS the oid; cache
hit IS the lookup; cache invalidation IS irrelevant (no two distinct
inputs can collide on the oid).

The integration shape: the author's existing cache table becomes a
view over an `@mirror/store`-shaped store, keyed by oid; the lookup
path stays the author's; the key derivation lifts from
"compute-from-inputs" to "read-the-oid." Byte-determinism in the
serialized input IS the requirement that was already implicit.

### 13.2 Binary stash for Dockerfile targeting → `shard` + `spectral_coordinate`

The MVP stash-and-target pattern wants Docker `FROM` lines to reference
prior build outputs by stable handle. The substrate has the typed
carrier: `shard.id: uuid_spectral` (`shards/glass.mirror` line 347) is
the navigable spectral identifier; `spectral_coordinate` (the wire
address derived in §4) is its reverse-DNS-namespaced wire surface.

Concrete pattern:

```dockerfile
# Author writes:
FROM mirror.local/cache/org.stagefreight.plan.binary.a1b2c3d4e5f6 AS upstream
# spectral_coordinate IS the immutable handle; the oid-short tail makes
# it cacheable by Docker's own layer cache; the namespace is the
# repository's commitment to its address space.

COPY --from=upstream /out/bin/stagefreight /usr/local/bin/
```

The Dockerfile's `FROM` clause references a `spectral_coordinate`-shaped
handle; the registry shim resolves the coordinate to the stored shard
(via the `@mirror/store` lookup); the Docker build receives an
immutable, content-addressed upstream layer. The author keeps
Dockerfiles; mirror provides the typed handle.

### 13.3 Frame: accelerator, not burden

The substrate is not asking the author to add a new system. The
substrate is naming the typed shape of the system already being built:
oid-keyed caching IS splinter content-addressing; binary-stash-for-FROM
IS spectral-coordinate addressing; the Dockerfile pattern IS the
existing build flow with one substitution. Integration cost reduces
to declaration; the substrate's primitives discharge the work in
flight.

### 13.4 What works today vs what's forward-promised

**Substrate-pull-honest framing of the caching/stash discharge:**

| Piece | Status today |
|---|---|
| `splinter.content: oid` carrier | Landed (`shards/glass.mirror`) |
| `shard.id: uuid_spectral` carrier | Landed (`shards/glass.mirror`) |
| `spectral_coordinate` wire address | Landed (this spec §4) |
| `@mirror/store` realisation (full read/write API) | Forward-promised (mirror task #268) |
| Dockerfile `FROM mirror.local/cache/...` registry shim | Forward-promised pattern (not currently executable) |
| Cache hit / miss flow against a live mirror store | Forward-promised (depends on #268 + registry shim) |

The Dockerfile example in §13.2 is a **forward-promised pattern**,
not a copy-pasteable working snippet at v0.1. The substrate-decl
describes the typed shape the integration WILL take; the live shim
lands when `@mirror/store` realisation does (mirror PR-2 territory).

What the author CAN do today: structure their MVP caching against the
oid + spectral_coordinate typed shape so the future shim is a
mechanical wire-up, not a redesign. The spec IS the alignment artifact;
the runtime plumbing follows.

---

## §14. MVP scope: multi-language cascade architecture in-scope; Purescript→npm as Stage-1 instance

The MVP boundary needs a structural defense against the runtime
scope-creep gravity (JVM + NPM + Python all at once). The framing
shift on 2026-06-23: **the PR ships the multi-language cascade
architecture per `shards/cascade.mirror` recognition #95 (the @cascade
family-root); Purescript→npm is the Stage-1 INSTANCE, not the
substrate itself.** This section draws the boundary, names the
architecture-vs-instance distinction, and references Mara's broader
survey for the species landscape.

### 14.1 Stage-1 in-scope (the MVP shape)

1. Native binaries (the existing MVP work).
2. Content-addressed cache via `splinter.content: oid` (§13.1).
3. Dockerfile `FROM`-by-`spectral_coordinate` (§13.2).
4. **The @cascade family-root substrate-decl** (`shards/cascade.mirror`,
   landed mirror-side 2026-06-23): the parametric primitive
   `cascade<source_grammar, target_grammar>` with `compile`, `measure`,
   and `cascade` actions, the `loss_lens` carrier, and the
   `cascade_well_defined` composed bilateral. This is the architecture.
5. **The Purescript → typed-JS → npm cascade** as the **Stage-1
   species instance** at `shards/cascade/purescript-npm.mirror`
   (forward-promised). One concrete realisation of the architecture;
   the author ships against this instance; future species shards
   extend the architecture without re-architecting.

The cascade architecture is the structural move that dissolves the
"which runtime" question. The architecture admits arbitrarily many
typed-source → mainstream-target cascades as additive species shards;
the Stage-1 instance gives the author broad npm reach immediately;
the Stage-2+ roadmap (§17) is decoupled from the architecture's
stability. The author ships ONE substrate-decl (the family-root) plus
ONE species (Purescript→npm) and the rest is forward-promised
additively.

Reference: Mara's typed-alternatives cascade survey
(`docs/research/2026-06-23-typed-alternatives-cascade-survey.md`,
commit `ecc471a` on mirror) maps ten mainstream stacks with three
confirmed counterexamples and one degenerate sub-pattern; the
survey IS the substrate-evidence base for the architecture-as-
primitive recognition.

### 14.2 The Purescript → typed-JS → npm cascade (operational shape)

The cascade is three lifts composed:

1. **Source → typed module:** Purescript source builds via spago (the
   community-standard build tool) or pulp (legacy) into typed JS
   modules at `output/<Module.Path>/index.js`. Module identity is the
   Purescript module path; type guarantees survive into the emitted JS
   as discipline (the typed-JS layer doesn't re-validate types, but
   the build is GREEN-only if the types hold).
2. **Module → cache:** mirror computes the splinter oid over the
   module's emitted output (the bundled JS plus its FFI dependencies);
   `spectral_coordinate` derives from `(oid, projection_kind="npm")`;
   subsequent builds hit cache by oid; no recomputation.
3. **Cache → npm artifact:** mirror's npm-projection species wraps
   the cached module output as an npm package (package.json + the
   bundled JS); arbitrary downstream npm projects consume via
   `npm install`; the consumer side IS standard npm with no special
   awareness of mirror.

The cascade preserves the substrate-pull discipline at each lift:
oid-by-content at lift 2; reverse-DNS namespacing at lift 3;
projection-species discipline (the open-universe pattern from §8.5)
admits future projection variants (typed-JS-bundle, ESM, CommonJS)
additively.

### 14.3 Why Purescript first (substrate-pull rationale)

Three substrate-pull reasons, each load-bearing:

1. **Functor/monad/applicative as first-class.** Purescript's type
   system carries the parametric algebra the substrate already
   speaks (recognition #51's expanding Hilbert space; the
   `labeled<X>` functor primitive forward-promised in #93 H4). The
   integration composes; Purescript's algebra IS the substrate's
   algebra at the consumer altitude.
2. **Pure functional.** Effects are typed; the Purescript module's
   identity is byte-deterministic in its source; the oid derivation
   in §13.1 hits cache reliably because there's no hidden state
   leaking through the type system.
3. **Compiles to JS without inheriting JS type-soup.** The typed-JS
   output is the substrate's gateway to the npm ecosystem; the
   consumer side reaches arbitrary npm projects; the producer side
   never touches untyped JS as authored code.

### 14.4 Post-MVP forward-promised (out of Stage-1 scope)

Each additional cascade species — F#→NuGet, Scala→JVM, Gleam→Hex+npm
dual, Elm→JS, Crystal→native, others — is a separate species shard
at `shards/cascade/<source>-<target>.mirror`. **None are
architectural changes; they are additive instances of the @cascade
family-root contract.** Each is forward-promised per the species
roadmap in §17; none ships in Stage-1.

Substrate-pull rationale for the deferral: each cascade has its own
grammar pair (Scala 3 → JVM bytecode has higher-kinded types erasing
to generics + reflection; F# → IL preserves discriminated unions via
tagged-pair encoding; Gleam → BEAM loses type discipline at Erlang
interop). The `loss_lens<source, target>` measurement is per-pair;
multi-cascade Stage-1 would commit to multiple per-pair discharges
simultaneously and inflate the species surface beyond what review can
carry in one PR.

The architecture decouples this. The @cascade family-root admits any
future species without spec rewrite; the bilateral discipline carries
forward; each new species lands as an isolated tick. The Purescript
cascade reaches npm WITHOUT requiring raw NPM substrate-decl — same
argument applies cascade-by-cascade: each typed alternative reaches
its mainstream target without requiring the mainstream side itself to
adopt mirror.

### 14.5 The boundary defense (explicit)

If the question "should we add JVM/NPM/Python to MVP?" surfaces, the
substrate-pull-correct answer is: **no.** Stage-1 ships Purescript +
the npm cascade. JVM is post-MVP; raw NPM is post-MVP (the cascade
covers npm consumers without it); Python is post-MVP. The cascade is
the architectural commitment that makes "ship one runtime, reach
many consumers" structurally possible.

---

## §15. Cascade substrate-decl (family-root LANDED; Purescript→npm as first species)

The substrate ground for the cascade architecture is the @cascade
family-root at `shards/cascade.mirror` (mirror, 2026-06-23,
recognition #95 candidate). The family-root declares the parametric
primitives (`grammar`, `typed_source`, `compiled_artifact`,
`loss_lens`, `information_loss`), the three load-bearing actions
(`compile`, `measure`, `cascade`), and the composed bilateral
(`cascade_well_defined` composing `grammar_coherent` and
`loss_well_defined`). The family-root IS the architecture.

This section names the **Purescript→npm species instance** that
specializes the family-root for Stage-1. The species shard
(`shards/cascade/purescript-npm.mirror`) is forward-promised; this
spec pins the contract shape the StageFreight side will admit when
it lands. The carriers, actions, and bilateral below specialize the
family-root primitives; they do NOT replace them.

For the family-root contract (the architecture), read
`shards/cascade.mirror` in the mirror repository directly. The
following subsections show how the Purescript→npm species discharges
at the species altitude.

### 15.1 Carriers

```mirror
# Purescript module — the typed source unit. Identity IS the module
# path under the Purescript namespace discipline (e.g. `Data.Maybe`,
# `StageFreight.Cache.Key`). Bare-ref at the floor; refinement to a
# typed record (path, dependencies, source-oid) forward-promised once
# the cascade lands a second projection.
type purescript_source = ref

# Purescript module — the compiled output of a Purescript source under
# spago/pulp. Identity IS the module path resolved to its output
# location (e.g. `output/Data.Maybe/index.js`). Carries the typed-JS
# emission; the type guarantees from the source survive as build
# discipline (the emitted JS is GREEN-only when types hold).
type purescript_module = ref

# npm artifact — the npm-package-wrapped form of a Purescript module
# ready for consumer install. Lifts purescript_module via the
# labeled<X> functor (per recognition #93 H4: substrate already
# supports parametric carriers; labeled<v, m> = annotated(v, m)).
# The label dimension carries the npm-side identity (package name,
# version, semver range) without losing the underlying
# purescript_module typed-content reference.
type npm_artifact = labeled<purescript_module>
```

**Forward-promise note (per Seam adversarial review 2026-06-23):** the
`labeled<>` functor primitive itself is forward-promised at
`shards/labeled.mirror` per recognition #93 H4 PARTIAL. The substrate
ALREADY supports parametric carriers (`imperfect<a,e,l>`, `option<a>`,
`result<a,e>`, `transparency<p>` all landed via `shift(T)`/`settle(T)`
infrastructure); the specific `labeled<v,m>` substrate-decl shard
lands as a small follow-up (~50-line shard composing the existing
parametric pattern). Cascade-tick order: `shards/labeled.mirror` lands
before the Purescript species shard discharges this `npm_artifact`
carrier as load-bearing.

The `labeled<purescript_module>` lift is the substrate-pull-correct
move: it composes with mirror's parametric algebra (recognition #51;
H4 functor primitive); it preserves the underlying typed reference;
it admits npm-specific labeling (name, version) without polluting the
purescript_module carrier with npm-only fields.

### 15.2 Actions

```mirror
# compile — source to typed module. Pure; deterministic in source;
# oid-stable in output. The bilateral discharge runs the Purescript
# type-checker and emits GREEN-only output; type errors discharge as
# DEFENSIVE verdicts.
compile(source: purescript_source, p: perturbation) -> purescript_module
  requires purescript_well_typed(source, p)
{ \ }

# bundle — module to npm artifact. The labeling step. Wraps the typed
# output with npm-side metadata (package.json shape, semver, entry
# point). The labeling preserves the underlying purescript_module
# oid; the npm artifact's identity is the labeled tuple.
bundle(module: purescript_module, label: ref, p: perturbation)
  -> npm_artifact
  requires npm_consumable(module, label, p)
{ \ }

# resolve — consumer-side lookup. Given an npm package name, resolve
# to the underlying purescript_module via the labeled<> projection.
# The consumer never needs to know about the Purescript source; the
# typed-JS output is what npm hands them.
resolve(name: ref, registry: ref) -> purescript_module { \ }
```

### 15.3 Bilateral

```mirror
# cascade_well_formed — the composed bilateral for the Purescript →
# npm cascade. Discharges BOTH sub-predicates: the Purescript side
# (source compiles GREEN; types hold) AND the npm side (the bundled
# artifact is structurally consumable by standard npm tooling).
cascade_well_formed(artifact: npm_artifact, p: perturbation) -> verdict
  requires purescript_well_typed(artifact, p)
  requires npm_consumable(artifact, p)
{ \ }
```

The bilateral composes two substrate-altitude properties; both must
discharge BOUNDED for the cascade to be wire-survival-valid. Short-
circuits on first DEFENSIVE per the standard composition discipline
(§2.5).

### 15.4 Honest hedges on the cascade

1. **spago vs pulp output shape.** spago (the modern community
   standard) and pulp (legacy) emit module outputs at slightly
   different paths (spago: `output/<Module>/index.js`; pulp: similar
   but with FFI bundling differences). The MVP commits to spago; pulp
   support is forward-promised. The substrate-decl admits both as
   build-backends behind the `compile` action; selection lives in the
   shard-altitude config, not the contract.
2. **Module-path-to-oid mapping.** The oid derivation must be
   deterministic in module content but stable across module-path
   renames within the same content (a moved module shouldn't change
   the oid). Floor commitment: oid is BLAKE3 over the canonicalized
   emitted-JS content + transitive FFI dependencies; module path is
   labeling metadata. The exact canonicalization is forward-promised;
   the MVP can ship with raw emitted-JS content as an honest hedge.
3. **Dockerfile FROM resolution.** The registry shim that resolves
   `spectral_coordinate` to a Docker layer must handle the Purescript
   bundle as a multi-file artifact (the npm package + the typed-JS +
   the FFI). The MVP commits to tar-bundling the npm package as the
   Docker layer payload; multi-layer optimization is forward-promised.
4. **FFI boundary discipline.** Purescript's FFI escape hatch allows
   raw JS at module boundaries. The MVP admits FFI but requires the
   FFI'd JS to be byte-deterministic (no `Date.now()`, no random IDs);
   the bilateral `npm_consumable` discharge includes an FFI-purity
   check. Authors who want non-deterministic FFI step outside the
   substrate-pull-correct cascade explicitly.

### 15.5 What this substrate-decl does NOT do at MVP

- Does NOT commit to a specific Purescript compiler version (spago
  pins it; the substrate stays version-agnostic at the carrier
  altitude).
- Does NOT support raw-NPM authoring (the cascade goes one way:
  Purescript → npm; not npm → mirror).
- Does NOT prescribe an FFI policy beyond the byte-determinism floor.
- Does NOT touch the existing StageFreight runtime; the cascade
  composes through the wire protocol established in §2-§5.

---

## §16. Mathematical formalization: loss as substrate primitive

This is the "math, not vibes" delivery, per Alex's 2026-06-23
verbatim to the StageFreight author: *"Building the multi-language
translation layer right now (math, not vibes). That's the PR."*

**Framing note (substrate-pull-honest, per Seam adversarial review
2026-06-23):** this section names the TYPED SURFACE the mathematical
discharge operates against — it does NOT claim that the Purescript→npm
loss number is already computable. The substrate-decl shape is
ratified at v0.1; the per-cascade discharge (actual loss numbers for
actual programs) is at species-altitude (§16's closing note explicit).
Read the five pieces below as the architectural contract the
computation will discharge against, not as a delivered measurement
engine. The math LIVES at recognition #51 (mirror as expanding Hilbert
space), [[feedback-loss-from-epistemologic-properties]] (loss as
@epistemologic/properties composite), and the forward-promised species
shards; THIS spec names the substrate that holds the math, not the
math itself.

The @cascade family-root operationalizes loss measurement as
substrate-typed data, not estimation. Five concrete pieces:

**1. `loss_lens<source_grammar, target_grammar>` is the measurement
instrument.** Per `shards/cascade.mirror` (lines 203–221), the
`loss_lens` carrier is parametric over (source_grammar,
target_grammar) and IS an instance of the `labeled<>` functor
(recognition #93 H4). Construction pairs the two grammars; the lens
IS its pair. Identity contract: byte-equality on the underlying
ref.

**2. Compilation is a functor; loss IS dimension reduction.** Each
cascade pairs a source grammar S with a target grammar T such that
compilation is a structure-preserving map `compile: S → T`. S admits
more grammatical structure than T preserves at runtime (S has
higher-kinded types, row polymorphism, discriminated unions; T has
bytecode, dynamic strings, primitives). Per recognition #51 (mirror
as expanding Hilbert space): each typed feature in S is a dimension;
compilation projects to fewer dimensions in T; **loss IS the
dimension reduction**. This is information-theoretic by construction,
not by analogy.

**3. `measure(source, artifact, lens, p) ->
imperfect<artifact, error, information_loss>`** is the substrate-typed
measurement primitive. Inputs: the typed source, the compiled
artifact, the loss lens pairing the grammars, and a perturbation. The
return is `imperfect<>` — the substrate's three-slot carrier for
(value, error, loss); the loss slot carries the measured
`information_loss` as substrate-typed data. The `requires
loss_well_defined(lens, source, p)` precondition forecloses
measurement against an incoherent lens (e.g., Purescript source
measured against a JVM bytecode lens — the substrate refuses).

**4. Loss IS a composite of @epistemologic/properties.** Per
[[feedback-loss-from-epistemologic-properties]] (Alex,
standing-feedback): at every Fate-tournament altitude, loss is a
composite of @epistemologic/properties. **Not Shannon. Not Dark. Not
invented.** The `information_loss` carrier holds the composite. The
per-cascade discharge (species altitude) selects which
@epistemologic/properties compose for that grammar pair.

**5. Per-program loss becomes substrate-typed data the author can
READ.** The author's Purescript program cascades through to npm; the
`cascade` action returns `imperfect<compiled_artifact, error,
information_loss>`; the loss slot is interrogable substrate-typed
data. The author asks "what gets erased at runtime?" and gets a
substrate-typed answer instead of a vibe. This is the operational
surface of "math, not vibes."

**Connection to @kintsugi.** The @kintsugi family operates on loss
(recognition #59: kintsugi loop altitude-portable). `@cascade`
declares the loss substrate; `@kintsugi` heals it. The cascade's
measurement output is precisely what @kintsugi consumes; the two
families compose at the loss boundary by construction.

**Honest hedge: per-cascade discharge is at species-altitude
(forward-promised).** The family-root declares the SHAPE of the
measurement. The actual computation — how Purescript ↔ JS
information-theoretic gap is computed bit-for-bit — discharges in
the per-species shard (`shards/cascade/purescript-npm.mirror`,
forward-promised). The architecture is mathematical; the per-cascade
realisation is incremental. Don't read this section as a claim that
the Purescript→npm loss number is already computable; read it as the
typed surface the computation will discharge against when the species
shard lands.

---

## §17. Cascade species roadmap

The @cascade family-root admits arbitrarily many typed-source →
mainstream-target cascades as additive species shards. Stage-1 ships
the architecture + one instance; Stage-2+ extends the instance set
without re-architecting. Reference: Mara's typed-alternatives
cascade survey
(`docs/research/2026-06-23-typed-alternatives-cascade-survey.md`,
mirror commit `ecc471a`) covers ten mainstream stacks.

### 17.1 Stage-1 (THIS PR)

| Species | Source grammar | Target grammar | Status |
|---|---|---|---|
| `cascade<purescript, npm>` | Purescript (row polymorphism, HKT, type classes) | npm (JS modules + package.json) | IN this PR (§15) |

### 17.2 Stage-2 candidates (Mara survey top 3)

| Species | Source grammar | Target grammar | Substrate-pull rationale |
|---|---|---|---|
| `cascade<rescript, npm>` | ReScript (sound types, fast compile) | npm (JS / ES modules) | Parallel npm reach with different type-discipline tradeoff; survey §2.3 |
| `cascade<gleam, beam_plus_js>` | Gleam (sound types, no exceptions) | BEAM bytecode AND JS — **dual target** | **Load-bearing rare shape:** one source, two simultaneous targets. Validates the parametric `cascade<S, T>` form trivially — same source, two cascade species. Per `shards/cascade.mirror` lines 116–120 |
| `cascade<fsharp, nuget>` | F# (discriminated unions, units-of-measure, computation expressions) | NuGet (.NET IL packaged) | Cleanest cascade instance on .NET; active Microsoft support; survey §2.2 |

*Stage-2 candidates listed above are pending Pack-peer verification of
the build-chain claims per survey §5.1; the architecture admits them,
but roadmap commitment requires independent verification beyond Mara's
N=1-per-stack survey.*

### 17.3 Stage-3+ (forward-promised additively)

Scala→JVM (sbt + Maven Central); Kotlin→JVM (Gradle + Maven Central);
Elm→JS bundle (Elm package registry); Crystal→native binary; others
per survey. Each is a single-species tick; none requires
architectural change.

### 17.4 Counterexamples (honest naming)

The survey surfaced three confirmed cases that **do NOT fit** the
source-cascade pattern. Naming them prevents the architecture from
over-claiming:

| Non-cascade | Why it doesn't fit |
|---|---|
| Crystal / Ruby | Shared culture, not a source-cascade runtime. Crystal compiles to native, not Ruby. The cascade `cascade<crystal, ruby>` does not exist as a compilation functor. |
| Hack / PHP | HHVM dropped PHP compat ~2017–2018. Hack no longer compiles to mainstream PHP. The cascade dissolved historically. |
| Oil & Nushell / Shell | These are REPLACEMENT runtimes for Bash, not source cascades that compile to Bash. Different architectural pattern. |

### 17.5 Degenerate sub-pattern (handled at species altitude)

**Language-internal strictness** (TypeScript-strict, Mypy-strict,
Sorbet, Psalm, C# nullable). Same language with stricter dialect;
the cascade collapses S and T into the same grammar with measure
approaching zero. The architecture admits these as cascade species
where `source_grammar` and `target_grammar` reference the same
underlying grammar at different strictness altitudes; the
`information_loss` is structurally zero for in-strict-mode programs
and non-zero where strict-mode features are absent in the relaxed
dialect. Worth naming as a distinct cascade-shape; handled at species
altitude per `shards/cascade.mirror` lines 109–114.

---

## §18. Demonstration artifact

The spec carries the contract; a concrete example carries the proof
that the contract is operational. This section forward-promises the
shape of that example.

### 18.1 Forward-promised example: `examples/purescript-cascade/`

A minimal end-to-end demonstration of the cascade. Directory shape:

```
examples/purescript-cascade/
├── README.md             — what this demonstrates; how to run
├── spago.yaml            — spago build config (Purescript side)
├── src/
│   └── Cache/
│       └── Key.purs      — minimal Purescript module (one function)
├── output/               — spago build output (gitignored)
├── package.json          — npm wrapper (consumer side)
└── consumer/
    └── index.js          — minimal npm consumer that imports the
                            bundled typed-JS output
```

The example demonstrates: (1) Purescript source compiles via spago;
(2) mirror computes the splinter oid over the output and hits cache
on rebuild (no recompile); (3) the npm package wraps the typed-JS
output; (4) a downstream consumer in `consumer/` imports the package
and uses the typed function.

### 18.2 Staging

Stage-1 PR-A (this PR family) ships the spec (§§1-§19) and **may**
ship the example skeleton if scope permits without contradicting the
"thin adapter shape" framing of §1.2. The fully working example —
spago build runs GREEN, mirror cache hits demonstrate, npm consumer
imports work — may land as PR-B once the Purescript-side species
shard lands on the mirror repository (the family-root has already
landed at `shards/cascade.mirror`, 2026-06-23).

PR-B's example is the demonstration artifact that makes the cascade
concrete for the author. PR-A's spec is the substrate-decl contract
that makes PR-B's example structurally sound.

---

## §19. Why this lands with a bang

**What ships in PR-A is the SPEC naming all five. The implementations
are the forward-promised tail (PR-B Go adapter; mirror PR-2 bytes-on-
wire; species shards for each cascade; @mirror/store realisation).**
The convergence below is the substrate-decl contract converging; the
running code follows in subsequent ticks per §11.6 cascade order.

The single PR demonstrates **five** things at once:

1. **Substrate-decl contract** (§2–§5 + §15): the wire-protocol
   carriers, actions, and bilateral, plus the cascade family-root
   contract specialized for Purescript→npm.
2. **Concrete pain solved** (§13): the author's caching work IS
   content-addressing; the binary-stash work IS spectral-coordinate
   addressing. The MVP work in flight IS the substrate's typed shape.
3. **Architectural scope protection** (§14): the @cascade family-root
   is the architecture; Purescript→npm is the Stage-1 INSTANCE; all
   other cascades (JVM/F#/Gleam/etc.) are additive species shards,
   not architectural changes. Future runtimes don't require spec
   rewrites.
4. **Cascade reach** (§14.2 + §17): ship the architecture once;
   reach the WHOLE typed-runtime → mainstream-ecosystem landscape
   incrementally as species shards land. Stage-1 demonstrates via
   Purescript→npm; Stage-2+ extends without re-architecting; the
   counterexamples (§17.4) are honestly named so the architecture
   doesn't over-claim.
5. **Mathematical formalization** (§16): loss as substrate primitive
   via the `loss_lens<source_grammar, target_grammar>` measurement
   instrument and the `measure(source, artifact, lens, p) ->
   imperfect<artifact, error, information_loss>` typed action. The
   author asks "what gets erased at runtime?" and gets a
   substrate-typed answer. Per Alex 2026-06-23: **math, not vibes.**

The bang IS the convergence: one PR closes the loop on caching, on
typed-runtime architecture, on consumer reach across mainstream
ecosystems, on scope-creep gravity, AND on the loss-measurement
formalization that makes per-program information loss substrate-typed
data. The substrate-pull-correct shape was already implicit in the
author's work AND in the broader typed-alternatives landscape; the
spec names both; the cascade architecture carries them.

---

— Mara, 2026-06-23
