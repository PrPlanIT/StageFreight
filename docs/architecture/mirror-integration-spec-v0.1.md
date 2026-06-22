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

This PR is **consumer-side substrate-decl preservation in Go**. It
mirrors the typed contract from `shards/io/stagefreight.mirror` into Go
type shapes and validation functions, and wires them through the
existing StageFreight runtime as a thin adapter package. It is the
twin of mirror PR #1 on the Go side of the same wire.

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
    return Bounded()
}
```

`Env` packages the runtime dependencies (store view, projection
registry) per StageFreight's "all inputs explicit, no package vars"
discipline (`docs/architecture/boundaries.md`, "Service Function
Rule"). The Cobra adapter (when one lands) builds `Env` from
flag/config inputs.

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

// Freight builds a FreightManifest after discharging the two requires
// clauses from the mirror substrate-decl: address_well_formed and
// invariant_preserved. The @magic contract discharge is forward-
// promised at v0.1 — the contract/promise inputs are accepted as
// opaque carriers and not yet validated structurally on the Go side.
// This is consistent with mirror's "freight is the FIRST consumer of
// invariant_preserved" framing: the cross-family bilateral lands on
// the Rust realisation; the Go side accepts the carrier shape.
func Freight(req FreightRequest) (FreightManifest, Verdict)
```

Note the deliberate Go-side opacity on `MagicContract` and
`MagicInvariant`. The mirror substrate-decl makes them load-bearing
through @magic family imports; on the Go side they are placeholder
newtypes until the cross-family bilateral discipline matters at
realisation time. This is an honest forward-promise, not a hidden
gap — it is named in §8 (what this PR does not do).

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
│                       ProjectionRegistry interface
├── env_test.go
└── magic.go            MagicContract + MagicInvariant placeholder
                        carriers (forward-promised opacity)
```

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

— Mara, 2026-06-22
