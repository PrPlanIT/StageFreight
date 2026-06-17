# StageFreight Signing — Trust Model & Implementation Plan

> **Status: living design document.** This is the durable, iterable home for StageFreight's
> signing architecture. It has been pressure-tested across many rounds (architecture, security
> boundary, operational reality, three adversarial falsification passes). Iterate it *here*.
>
> **Commit 1 — the signer seam — is SHIPPED through the renderer (1a–1e):** config schema + graph +
> validation and the pure IR + `Compile` (`9da9deb`), and the cosign renderer `Render`/`Env`/
> `resolveKMSURI` (`6e539d2`). The cosign **executors** + their wiring (`df73062`) are also SHIPPED:
> images are signed after push and `SHA256SUMS` after `WriteChecksums`, proven end-to-end against
> cosign v3.0.6 by `cosign.TestSignBlobEndToEnd`. **Operational target (gated):** the full
> self-hosted / always-on / managed-infrastructure plan — and the ordered gap-closure roadmap that
> makes it real — is **Part O ("Operational trust model")** below; release `SHA256SUMS.sig` is
> currently produced-but-stranded (O.6/O.9-#1). Terminology: the config object is a **`signing_profile`**
> (was "signing_policy"); assurance is **flat** (`physical_presence`/`non_exportable` keywords on the
> profile), not a nested capabilities block. Sections below are tagged ✅ SHIPPED / ⏳ PENDING.

## Context

StageFreight already signs image digests with cosign, but the signer is hardcoded
to `--key <path>` (`src/build/docker/sign.go:27-31`), `--tlog-upload=false`, and an
implicit key discovered from `COSIGN_KEY` / `.stagefreight/cosign.key`. There is no
way to select a signing *method* (hardware/YubiKey, keyless/OIDC, KMS) and no way to
sign the release bundle (`SHA256SUMS` is produced unsigned at `archive.go:160-182`).

This delivers the two foundational pieces of the agreed "layered" signing model:

1. **A signer seam** — a top-level `signing_profiles:` block of named profiles (`requires` =
   trust class *intent*: `key` | `oidc` | `kms` | `hardware`), referenced per-target by
   `signing_profile: <id>` (the same reference-by-id pattern as `registry:` →
   `FindRegistryByID`). A profile compiles to a neutral `SignPlan`, rendered to cosign — no
   hardcoded invocation.
2. **Release-artifact signing** — sign `SHA256SUMS` via `cosign sign-blob`,
   record it in the results manifest, and auto-attach `SHA256SUMS.sig` to the release.

The insulation boundary is the `SignPlan` IR + `Compile` (data + a pure compiler), **not** an
interface/registry. cosign is a renderer of the plan at the edge — swappable by adding another
renderer, never a model participant.

Explicitly **out of scope** (deferred — the IR is shaped not to foreclose them): approval gates /
publish blocking, policy-selector DSL, channel taxonomy, timeout/fallback chains, **multi-trust
composition** (`requires: [hardware, oidc]`; parses but v1-errors), pipeline-side verification, and
**additional renderers of `SignPlan`** (vault-transit, pkcs11-native, sigstore-go) — added as
concrete renderers *only when they exist*, with no plugin/registry pre-built (YAGNI). `requires` is
scalar-or-list and the profile is a named object, so all are additive.

## Locked decisions

- **`requires` names the trust class — that is all profile carries.** A profile declares the
  trust *requirement*; it never names a device, vendor, transport, or provider. Classes:
  `key`, `oidc`, `kms`, `hardware`. `requires` is scalar-or-list (v1 enforces exactly one;
  multi-trust `[hardware, oidc]` parses but v1-errors as "deferred"). Aliases `keyless→oidc`,
  `yubikey→hardware` normalized in `Normalize`; `yubikey`/`fido2`/`vault`/`aws` are rejected
  as classes — they are machinery.
  ```yaml
  signing_profiles:
    - id: ci-oidc
      requires: oidc                                  # identity-backed ephemeral signing
      oidc: { issuer: "...", identity: "..." }        # expected signer identity (optional)
    - id: maintainer
      requires: hardware                              # non-exportable key + physical presence
      physical_presence: required                     # flat assurance keyword (hardware-class only)
      non_exportable: required                        # flat assurance keyword (hardware-class only)
      attestation: true
    - id: org-automation
      requires: kms                                   # managed/remote key custody
      kms: { ref: release-signing-key }               # LOGICAL ref — bound to a URI at render time
    - id: release-key
      requires: key
      key: { ref: "env:COSIGN_KEY" }                  # "path" or "env:VAR"
      transparency_log: false                          # optional override (any class)
  targets:
    - { id: dockerhub-stable, kind: registry, build: stagefreight, signing_profile: maintainer }
  ```
  **No machinery in the profile.** The hardware *transport* (FIDO2 `--sk` vs PKCS#11) is selected at
  render time from what the environment offers, gated by the required **assurance keywords**
  (`physical_presence`/`non_exportable`, value `required`) — never named in the profile. KMS uses a
  **logical ref** bound to a URI at render time (`resolveKMSURI`, 1e), so the provider/URI lives in
  deployment wiring, not the trust profile.
- **Legacy is a default profile, not a parallel code path.** `Normalize` synthesizes an implicit
  `id: legacy, requires: key, key.ref: env:COSIGN_KEY`. A target with no `signing_profile`
  compiles to the `legacy` plan. ONE path — `Compile` over a profile — never a separate legacy
  branch in `sign.go`. The `legacy` plan is `Enabled` iff the key actually resolves
  (`ResolveCosignKey() != ""`), preserving today's no-key-no-signing.
- **Back-compat = implicit image signing ONLY.** A target with no `signing_profile`
  keeps today's implicit key-signing for images, byte-identical. The implicit key does
  **not** auto-sign `SHA256SUMS` (or SBOM, hardware, OIDC) — those require an explicit
  profile. "Existing configs produce identical outputs unless explicitly changed."
- **Sequencing = seam first (Commit 1), SHA256SUMS second (Commit 2).** The seam is
  the architecture; blob signing is a consumer. Prove the abstraction with an
  unchanged dogfood build before adding the consumer.
- **Compiler model, not a backend system. cosign is a pure output target.** The seam is a
  deterministic lowering through a pure neutral IR — `SigningProfile → Compile → SignPlan → render
  → cosign CLI` — **not** a `Backend` interface + registry. A registry/interface would be a second
  resolution graph that mirrors cosign anyway (the abstraction-inversion trap) and violates
  single-source-of-truth. The stable thing is the profile model + `SignPlan` IR (data); cosign is a
  renderer at the edge. No `ResolveBackend`, no `KMSResolver` interface, no plugin graph. A future
  native signer (Vault-Transit, PKCS#11, sigstore-go) is just *another renderer of `SignPlan`*,
  added when it exists — YAGNI until then.
- **`SignPlan` is *requirements* ("what must be true"), never cosign mechanism.** It carries
  required trust properties / references — never `--sk`, `--key`, URI schemes, or flag choices.
  The renderer does **capability satisfaction**, not "cosign mode selection": given the plan's
  requirements and a pure `Env` capability struct (what the runtime offers — KMS/FIDO2/PKCS#11),
  it emits an invocation that satisfies them. No URI or flag concept exists above the renderer.
- **Correctness split — the one principled boundary.** `Compile` owns *all* profile-level
  validation (class coherence, assurance-keyword rules, ref presence) and is total over valid config —
  static, env-independent. `Render` owns *only* env-satisfiability (Compile can't see whether a
  device is present) and must be **deterministic and loud**: hard-error when the `Env` can't
  satisfy the plan. No silent fallback selection.
- **Package placement:** `src/sign` (pure: trust types, `SignPlan`, `Compile`; imports `config`,
  `toolchain`, **never cosign**), `src/sign/cosign` (the cosign **renderer** — `Render(plan, op,
  env)` + declared `Env` + thin executor; the only cosign-aware package). `src/build/docker` and
  `src/cli/cmd` compile a `SignPlan` via `src/sign` and hand it to `src/sign/cosign`.
  (`src/security/verify.go`'s separate cosign-verify use is out of scope.)
- **transparency_log default:** off for `key`/`kms`/`hardware`, on for `oidc`; `transparency_log:` overrides.

## Foundational invariants frozen by the shipped renderer (1e)

Four load-bearing semantics are now realized in code (`6e539d2`) and MUST NOT erode as the executor,
verification, and composition layers accrete on top. Each is first-class, not an implementation detail:

1. **The ambiguity domain is trust-principal identity, NOT transport mechanism.** One principal
   reachable via multiple transports is *not* ambiguity — refusing it would break completely
   legitimate setups (a YubiKey via FIDO2 **and** PKCS#11; one KMS principal via multiple aliases;
   one OIDC identity through equivalent token paths). `Render` groups witnesses by `Principal`,
   counts the distinct set `|D|`, and refuses only `|D|>1` (genuinely *different* keys could each
   sign). A single principal with several reaches is resolved by a deterministic transport
   preference — trust-neutral, never an error, never a profile field. This distinction is the
   model's spine: refuse real *authorization* ambiguity while never accidentally refusing redundant
   *access paths* to one authority.

2. **`Env` is declared, never probed.** The deployment explicitly enumerates witnesses; the renderer
   selects deterministically over that fixed set and performs **no runtime discovery**. Same
   discipline as the rest of StageFreight — explicit declarative inputs, no ambient discovery,
   deterministic + reproducible + auditable evaluation — and it is what stops the renderer from
   slowly becoming a stateful capability-discovery subsystem. Hardware is therefore never implicitly
   adopted: plugging in a key cannot silently change signing behavior.

3. **The IR is assurance semantics only.** `SignPlan` carries *what must be true* (trust class +
   boolean assurance requirements + identity expectation + logical refs) and **zero cosign
   vocabulary** (no `--sk`/`--key`/URI/flag). The renderer is the sole translator from assurance to
   invocation; renderer-shaped fields must never leak back into the IR — that erosion is exactly
   what would collapse "cosign is an edge renderer."

4. **Publish owns signing orchestration.** `Render` decides *how a single signature is produced*; it
   does not decide *when / whether / how many* signatures a release acquires, nor sequence them
   against distribution. That orchestration — invoking the executors, ordering signing relative to
   push, recording outcomes, choosing publish-blocking vs best-effort — belongs in **Publish**, the
   post-distribution phase, never in the renderer or the IR. Freezing this now keeps the next slice
   (executor wiring) from smuggling release-lifecycle logic into the pure renderer.

## Enforced invariants (each is a test target, not just prose)

1. **No *device* ambiguity in render selection.** `Render` resolves over distinct trust principals
   (signing devices/keys), not transports. `|distinct devices satisfying the plan|`: `0 → error`,
   `1 → use`, `>1 → error` (genuinely different keys — a trust ambiguity that would silently drift
   and break "which key signed this?"). A single device reachable by multiple *transports* (same key
   via FIDO2 + PKCS#11 — identical trust) is resolved by a **deterministic renderer-internal**
   transport preference: trust-neutral, never an error, and never a profile field. `Env` is
   *declared*, not probed. Tests: two distinct devices satisfying the plan → error; one device via
   two transports → deterministic pick (no error).
   - **This does not fight CI fleets.** `Env` is *per node*, and devices group by **key identity**.
     A 100-node fleet where every node holds the *same* key is `|D|==1` per node — redundancy is
     fine, not ambiguity. The genuine `|D|>1` case is two *different* keys on one node — most often
     **key-rotation overlap** — which the env must narrow *explicitly* (not a silent pick), which is
     the whole point: during rotation, "old key vs new key signed this" must stay answerable.
2. **Assurance keywords are hardware-class-only.** `physical_presence`/`non_exportable` are invalid
   on any other class (1c), and their only legal value is `required`. Test: an assurance keyword
   under a `kms`/`oidc`/`key` profile → validation error.
3. **KMS is pure string substitution.** `resolveKMSURI` is `ref → env → URI` verbatim; no provider
   parsing/registry/semantics in core. Test: core packages contain no `vault`/`awskms`/`gcpkms`
   string; only the env value carries a scheme.
4. **`Compile` is pure and cannot fail.** Returns `SignPlan`, no error path. *All* validation is
   upstream in `config.Validate` (runs at audition). The renderer is the only runtime failure
   surface. Test: `Compile` over every validated profile never panics/errors; config validity is
   never a function of the runtime environment.
5. **Legacy is just another profile.** No special-case branch anywhere — the synthesized `legacy`
   profile flows through `Compile`/`Render` identically. Test: grep shows no legacy-specific code
   path in `sign`/`cosign`/`record_outcomes`; `legacy` plan renders byte-identical to today.
6. **Principal identity & verification are CLASS-SPECIFIC — the model spans two identity domains that
   do NOT collapse, and it must never pretend they do.** Soundness rests on correctly counting
   distinct principals; *how* a principal is identified and verified differs by domain:
   - **Cryptographic identity** (`key`/`kms`/`hardware`): the `Principal` *is* a public key, and it is
     **derived from the key material** (fingerprint), not merely declared. `Render` groups aliased
     witnesses (one key via PKCS#11 + KMS replica + HSM shard) by *computed* fingerprint → correctly
     `|D|==1`; a witness whose signing key ≠ its claimed identity is caught intrinsically. Equivalence
     here is **mathematical**, not an Env-honesty assumption.
   - **Claim identity** (`oidc`): the `Principal` is a `(issuer, subject)` **claim**, verified via the
     issuer's trust chain / JWKS / transparency log — **time-varying, no stable key**. It must NOT be
     checked by "signature public key == principal" (keyless has none). Full claim verification
     (cert chain, issuer trust, Rekor) is the reserved *verification* phase; v1 records the claim,
     does not adjudicate cross-issuer equivalence.
   So invariant 6 is **two rules, not one**. Tests assert the crypto rule (derived-fingerprint
   grouping; key≠identity → error) and that the oidc path is *not* run through the key check.
7. **Assurance keywords are labels until attested.** `non_exportable`/`physical_presence` are
   unverifiable strings unless bound to a **device-attestation proof** rooted in hardware
   (FIDO/TPM/YubiKey attestation) — a software TPM can assert them otherwise. The `hardware` class
   carries its claimed trust *only* when capabilities are attestation-backed; attestation binding is
   the reserved phase. v1 must be **honest that an un-attested `hardware` capability is a declaration,
   not a proof** (no silent overstatement of trust).
8. **The irreducible trust root is explicit, not eliminated.** Crypto verifies *consistency* (the key
   that signed is the declared key) — never *authority* (that this key is the legitimate release
   principal). The principal→authority binding is a config-trust assumption, the root every signing
   system has. The model makes it visible; it does not pretend to remove it.
9. **`Env` constrains mechanism, never identity.** `Env` may select/limit *transport*, but must never
   determine *principal identity* — it cannot decide which key is "real," which OIDC identity is
   valid, or which witnesses are "the same principal." Equivalence is **derived** (crypto: fingerprint)
   or **claim-defined** (oidc: `(issuer, subject)`), never Env-asserted. Test: an `Env` that labels two
   different keys as one `Principal` is rejected (fingerprints differ); an `Env` cannot collapse
   distinct keys or split one key.
10. **Transport selection never alters trust classification.** When one principal is reachable several
    ways, the deterministic transport preference picks one but the recorded trust class comes from the
    *profile + attestation*, never from *which transport was chosen*. Transport quality is not trust
    inference (a better-custody path never silently upgrades; a worse one never downgrades — and the
    capability filter already excludes witnesses that don't meet the required properties). Test: the
    same principal rendered via two transports yields identical trust classification.
11. **OIDC principals are never comparable across issuer boundaries** unless explicitly normalized by
    the (reserved) verification subsystem. `(issuerA, sub)` ≠ `(issuerB, sub)` — two issuers asserting
    the same subject are distinct principals, and no higher layer may treat OIDC identity equality as
    cross-issuer equivalence. Test: same subject under different issuers are distinct principals.
12. **Config validation is environment-agnostic.** `config.Validate` depends only on declared
    schema/graph — never on runtime `Env` shape (no `Validate(env)`). This seals Compile-purity:
    validation semantics cannot shift with the environment. Test: `Validate`'s result is a pure
    function of config alone.

**Grounding axioms** (close the ambiguities the invariants above *assume* — implied → enforced):
- **Env is a declared snapshot, not runtime discovery.** The deployment explicitly enumerates
  witnesses; `Render` selects deterministically over that *fixed* set and **never probes** the
  runtime. (Resolves the declared-vs-discovered dual-model firmly to *declarative* — required for
  reproducibility, audit, and test determinism. This is why hardware is never implicitly adopted.)
- **Principal canonicalization — identity derives from key material only, never access path.** Crypto
  principal = fingerprint of the key material; PKCS#11 slot vs file vs KMS endpoint vs loader path
  never changes it. OIDC principal = `(issuer, subject)` only. One canonical rule; no path-dependent
  identity. (This is what makes inv. 6's derived grouping unambiguous.)
- **Transport preference is a static, code-declared total order** — identical on every node (part of
  the renderer's determinism *and* auditability: "why PKCS#11 over FIDO2?" has a fixed answer), and
  trust-neutral (inv. 10). Never env-ordering (would vary per node) nor config (would leak mechanism
  into profile).
- **Assurance keywords are pre-attestation *constraints* in v1**, not verified facts — `Render` uses
  them as declarative filters; they become post-attestation *assertions* only in the reserved
  attestation phase (inv. 7). Future composition must never conflate the two meanings.
- **`config.Validate` is the single, deliberate semantic-correctness gate** Compile depends on — a
  known critical dependency by design (the alternative is the drift-prone double-validation we
  rejected). Its completeness is therefore itself a first-class test target, not a neutral fact.

---

## Commit 1 — The signer seam (refactor; default key path stays byte-identical)

### 1a. Config schema — `src/config/signing.go` (new) — ✅ SHIPPED (`9da9deb`)
A profile is expressed in trust vocabulary — **no cosign URIs, no devices, no machinery**. The
shipped shape uses **flat assurance keywords**, not a nested capabilities block:
```go
type SigningProfile struct {
    ID       string       `yaml:"id"`
    Requires StringOrList `yaml:"requires"`                       // trust class(es); v1 enforces exactly one
    Key  *KeyTrust  `yaml:"key,omitempty"`                        // {Ref}  — "path" or "env:VAR"
    OIDC *OIDCTrust `yaml:"oidc,omitempty"`                       // {Issuer, Identity} — both optional
    KMS  *KMSTrust  `yaml:"kms,omitempty"`                        // {Ref}  — LOGICAL ref, URI-bound at render
    PhysicalPresence string `yaml:"physical_presence,omitempty"` // assurance keyword "required" (hardware only)
    NonExportable    string `yaml:"non_exportable,omitempty"`    // assurance keyword "required" (hardware only)
    TransparencyLog  *bool  `yaml:"transparency_log,omitempty"`  // nil = class default
    Attestation      bool   `yaml:"attestation,omitempty"`
}
```
- Assurance is **flat keyword fields on the profile** (value `required`), *not* a nested
  `hardware.capabilities` enum — they describe required trust *properties*, never devices; the
  renderer (1e) selects a transport satisfying them. There is no `Capability` type.
- `Requires` parses scalar-or-list (`StringOrList.UnmarshalYAML`, scalar coercion).
  `validTrustClasses = {key, oidc, kms, hardware}`; alias normalization (`keyless→oidc`,
  `yubikey→hardware`) in `NormalizeSigning`, which also synthesizes the implicit `legacy` default
  profile (`requires: key`, `key.ref: env:COSIGN_KEY`). Mirrors the flat style of `RegistryConfig`.

### 1b. Wire into config graph — ✅ SHIPPED (`9da9deb`)
- `Config.Signing []SigningProfile yaml:"signing_profiles"` — `src/config/config.go` (after `Registries`).
- `TargetConfig.SigningProfile string yaml:"signing_profile,omitempty"` — `src/config/target.go` (after `Registry`).
- `FindSigningProfileByID` and `ResolveSigningProfileForTarget(t, profiles) (*ResolvedSigningProfile, error)`
  both live in `src/config/signing.go` (mirroring `FindRegistryByID` / `ResolveRegistryForTarget`).
  Returns the referenced profile; the synthesized `legacy` profile when `t.SigningProfile == ""`
  (single path — never nil for a signable kind); error `target %s: signing_profile %q not found`
  when set-but-unknown. `ResolvedSigningProfile` is the flattened view consumed by `Compile`.
- `NormalizeSigning` (alias canonicalization + `legacy` synthesis) runs at the top of `Normalize`,
  before the no-vars early return, so legacy synthesis happens even for a varless config.

### 1c. Validation — `src/config/signing.go` :: `ValidateSigningProfiles` — ✅ SHIPPED (`9da9deb`)
The single validation layer (`Compile`, 1d, is total over validated config and never re-validates).
Wired into `config.Validate` alongside `ValidateTargetSigningProfileRefs`. `Validate` runs **before**
`Normalize`, so validation is **alias-tolerant** (`keyless`/`yubikey` accepted, canonicalized via an
inline `normalizeTrustClass` without mutating input). Rules: empty/duplicate `id`; empty `requires`;
**`len(requires) > 1` → "multi-trust composition not yet supported"** (deferred, reserved); a
`requires` value outside `validTrustClasses` (machinery names `fido2`/`vault`/`aws`/`pkcs11` rejected
— they are transports/providers, not trust); class/field coherence (a `key`/`oidc`/`kms` block for a
class other than the declared one); **assurance keywords (`physical_presence`/`non_exportable`)
present when class ≠ hardware → error** (invariant 2), and their only legal value is `required`;
required refs per class (`key.ref` for key, `kms.ref` for kms). `ValidateTargetSigningProfileRefs`
checks each target's `signing_profile` resolves; the reserved `legacy` id is always valid. Runs at
audition — correctness static and upfront, never in the renderer. *(The target-kind restriction to
`registry`/`binary-archive` lands with the 1f/1g call-site wiring.)*

### 1d. The IR + compiler — `src/sign` (new package; pure, never imports cosign) — ✅ SHIPPED (`9da9deb`)
The stable model. Imports `config` only. The IR is **assurance semantics, never cosign mechanism**:
```go
type SignPlan struct {
    // ── Trust requirements (what must be true) ──
    TrustClass               Class               // key | oidc | kms | hardware
    TransparencyRequired     bool                // signature must be recorded in a transparency log
    RequiresPhysicalPresence bool                // signer must demonstrate physical presence
    RequiresNonExportableKey bool                // signing key must be hardware-bound / non-exportable
    Identity                 IdentityConstraints // {Issuer, Subject} — oidc/keyless expectation
    // ── Logical references (bound to keys/URIs only at render time) ──
    KeyRef string // key class: "path" or "env:VAR"
    KMSRef string // kms class: logical ref, URI-bound by the renderer
    // ── Execution modifier (kept distinct so profile logic never leaks here) ──
    Attestation bool
}
```
- The assurance keywords lower to **explicit boolean requirements** (`RequiresPhysicalPresence`/
  `RequiresNonExportableKey`), not a `[]Capability` enum. There is deliberately **no `--sk`,
  `--key`, URI, or flag concept** here — those are the renderer's vocabulary.
- `Op` is `OpSignImage` / `OpAttest` / `OpSignBlob`.
- `Compile(*config.ResolvedSigningProfile) SignPlan` — deterministic lowering, **total over
  validated config** (no error path; `config.ValidateSigningProfiles`, 1c, is the validation layer).
  Carries logical refs verbatim — "Compile is pure" means pure over the *logical* profile, never a
  claim that resolved execution is static. Transparency default: on for `oidc`, off otherwise,
  overridden by the profile's `transparency_log`. The `legacy` default compiles like any other.
- `Enabled(p SignPlan) bool` — a `key`-class plan is enabled iff its `KeyRef` resolves (preserving
  today's no-key-no-signing); other classes are enabled when a profile configured them. The only
  env-dependent function; `Compile` stays pure.
- `SignOptions{ MultiArch, PredicatePath }`; `SignatureResult{ SignatureRef, AttestationRef,
  SignaturePath }` — plain data passed to / returned from the renderer.

### 1e. The cosign renderer — `src/sign/cosign` (the ONLY cosign-aware package)
A **capability-satisfaction emitter**, not a mode selector. No interface, no registry.

**✅ SHIPPED (`6e539d2`): `Env`, `Render`, `resolveKMSURI`.**
- `Env` is a **declared** capability graph — explicitly enumerated witnesses, **never auto-probed**
  (declaring rather than discovering prevents a plugged-in key from silently changing signing
  behavior). The shipped shape, every witness carrying an explicit `Principal`:
  ```go
  type Principal string // public-key fingerprint (crypto classes); (issuer, subject) for oidc
  type Env struct {
      Keys   []KeyFile      // {Principal, Path}
      KMS    []KMSKey       // {Principal, URI}
      FIDO2  []FIDO2Device  // {Principal, PhysicalPresence, NonExportable}  — cosign --sk
      PKCS11 []PKCS11Slot   // {Principal, URI, PhysicalPresence, NonExportable} — cosign --key pkcs11:
      OIDC   []OIDCIdentity // {Issuer, Subject}
  }
  ```
  The `Principal` is the model's single load-bearing assumption made **explicit and represented in
  data**: identity equivalence is **declared, never inferred from transport/endpoint shape**.
- `Render(p sign.SignPlan, op sign.Op, env Env) (args []string, err error)` — **pure** given
  `(plan, op, env)`; a **constraint solver over distinct trust principals**. For the plan's class,
  let `D` = the distinct `Principal`s among the satisfying witnesses: `|D|==0 → error`
  (unsatisfiable); `|D|>1 → error` (genuinely different keys could each sign — a **trust** ambiguity,
  never a silent pick); `|D|==1 → use it`. **One principal reachable many ways is `|D|==1`** —
  multi-region KMS, an HSM pool, one key via FIDO2 **and** PKCS#11 — resolved by a **deterministic
  transport preference** (`--sk` before PKCS#11): a transport choice, not a trust choice, never an
  error, never a profile field. Hardware is where `Env` genuinely enumerates multiple physical
  witnesses, so the principal-grouping is load-bearing there; `key`/`kms` resolve their single
  referenced key; `oidc` is keyless (no `--key`). `Render` emits **trust + policy flags only** —
  `sign`/`attest`/`sign-blob`, `--key`/`--sk`, the cosign-v3 transparency flags
  (`--use-signing-config=false --tlog-upload=false` offline, `--use-signing-config=true` when a
  transparency log is required), and `--yes` for image
  ops; the op target (image digest, blob path, `--predicate`) is appended by the executor, which
  keeps `Render` pure and table-testable.
- Internally `Render` is `selectMechanism` (plan + env → the one satisfying mechanism) then `emit`
  (mechanism → cosign args) — both pure, both in `src/sign/cosign`, neither a domain abstraction.
- `resolveKMSURI(ref)` — **pure env substitution** (invariant 3): `ref → $SF_SIGN_KMS_<REF> → URI`,
  verbatim. No provider parsing/registry; core never knows vault/aws/gcp. `resolveKeyRef` mirrors
  `sign`'s `env:VAR`/path resolution at render time. The concrete URI/key never enters the profile
  or `SignPlan`.

**✅ SHIPPED (`df73062`): executors + invocation.** Concrete functions (not interface methods):
`SignImage(ctx, rootDir, desired, digestRef, plan, env, opts)`, `Attest(...)`, `SignBlob(ctx, …,
blobPath, plan, env) (sigPath, err)`, `Available(env)`. Each calls `Render`, then execs cosign via
`toolchain.Resolve("cosign", …)` with a class-specific `signEnv(p)`: hermetic `CleanEnv`+`COSIGN_YES=1`
for `key` (forwarding `COSIGN_PASSWORD` so an encrypted key is usable), plus sigstore/CI-OIDC token
vars for `oidc`, cloud-cred vars for `kms`, and the full host env for `hardware` (interactive — wired,
not unattended-CI-runnable). Invoked from the publish phase: `signImages` after a successful push
(maps each buildx `PushObservation` back to its target's profile by host/path; legacy default signs
when `COSIGN_KEY` resolves) and `signChecksums` after `WriteChecksums` (explicit profile only — the
legacy default never auto-signs blobs). **Publish owns this orchestration (foundational invariant 4);
the renderer does not.** Best-effort + loud: a failure records a failed outcome and warns, never aborts.

### 1f. Refactor call sites onto the compiler — `src/build/docker`
The cosign functions in `docker/sign.go` (`CosignSign`/`CosignAttest`/`CosignAvailable`, :15-93)
are **deleted**; logic moves to `src/sign/cosign`. `recordAttestationOutcomeIfConfigured`
(`record_outcomes.go`) does `plan := sign.Compile(profile)` (config already validated at audition)
then, if `sign.Enabled(plan)`, `cosign.SignImage(ctx, …, plan, opts)` / `cosign.Attest(...)`. The
call site holds a neutral `SignPlan` and invokes the cosign renderer at the edge — the profile→plan
half is fully cosign-free.
`ResolveCosignKey` and `signEnv` move to `src/sign/cosign`; `toolchain.CleanEnv` stays the hermetic
primitive the renderer reuses.

### 1g. Threading (single path via the `legacy` default profile)
- `ResolveSigningProfileForTarget(t, cfg.Signing)` returns the referenced profile, or the
  synthesized `legacy` profile when `t.SigningProfile == ""` — **never nil for a signable kind**.
  One resolution path; no separate legacy branch.
- `RegistryTarget.SigningProfile *config.ResolvedSigningProfile` — `src/build/plan.go:76-85`.
- Populate at the lowering seam `src/build/docker/image_engine.go:184-193`: after
  `ResolveRegistryForTarget`, call `ResolveSigningProfileForTarget(t, cfg.Signing)` and set
  `SigningProfile: sp` (propagate error like :152-154).
- `src/build/docker/execute.go:455-462`: the build-scoped `cosignKey` string is gone; the
  per-target loop passes `reg.SigningProfile` (always populated — `legacy` when unset) into the
  recorder, which compiles it via `sign.Compile`.
- `recordAttestationOutcomeIfConfigured` (`record_outcomes.go:119-171`): replace
  `cosignKey string` with `profile *config.ResolvedSigningProfile`; compute
  `plan := sign.Compile(profile)`; guard `if !sign.Enabled(plan) || digest == ""`; then
  `cosign.SignImage(...plan...)` / `cosign.Attest(...plan...)`.
- **Back-compat guarantee:** no `signing_profiles:` and no `signing_profile` ⇒ `legacy` profile ⇒
  `class:key`/`env:COSIGN_KEY` ⇒ the shipped renderer emits `sign --key <path>
  --use-signing-config=false --tlog-upload=false --yes` (cosign v3 offline key signing). Proven
  end-to-end by `cosign.TestSignBlobEndToEnd` (`-tags integration`): the executor signs a blob with a
  generated key and `cosign verify-blob` confirms it. (Signing was previously dormant — there is no
  live legacy invocation to be byte-identical to; the bar is "produces a verifiable signature.")

### Commit 1 verification
- Unit: `sign.Compile` (config profile → `SignPlan` requirements, every class, legacy default —
  total transform); config validation (1c) profile rules; `src/sign/cosign` `Render` table tests
  (every class × op, tlog polarity, override) **including the unsatisfiable-`Env` hard-error** (e.g.
  hardware required but `Env` offers neither FIDO2 nor PKCS#11; kms required but the ref is unbound);
  `resolveKMSURI` env lookup; config validation (id uniqueness, bad `requires`/machinery-name
  rejection, multi-trust deferred-error, capability/coherence, ref existence, kind restriction);
  `ResolveSigningProfileForTarget` found / not-found / legacy-default; `image_engine` populates
  `RegistryTarget.SigningProfile`; `legacy` plan renders to the same args as today.
- **Dogfood (the real proof):** build the dev image / run a docker build with the current
  implicit key; assert image signing is unchanged (same flags, signature present) — i.e. the
  refactor onto the compiler is behavior-preserving.
- `oidc`/`hardware` are wired + arg/env unit-tested but not exercised e2e in CI (no device / OIDC env).

---

## Commit 2 — Sign `SHA256SUMS` (consumer of the seam)

### 2a. `SignBlob` renderer — `src/sign/cosign`
`Op.SignBlob` is already in the IR (1d). Implement the executor `cosign.SignBlob`:
`Render(plan, SignBlob, env)` + `--output-signature <blobPath>.sig` + positional `blobPath`;
same toolchain resolution + `signEnv`. Greenfield (cosign `sign-blob` unused today). Call sites
compile a `SignPlan` and call `cosign.SignBlob(ctx, …, path, plan)`.

### 2b. Results manifest — `src/artifact/results.go`
`OutcomeTypeBlobSignature = "blob_signature"` (add to const block :77-82 and `Valid()` :85-91);
`Outcome.BlobSignature *BlobSignatureOutcome` (:64-72); `BlobSignatureOutcome{ Status, Kind,
BlobPath, SignaturePath, Class, Error }`. `outcomeTypeHasTarget()` (:98) returns false
(un-targeted, like binary/archive — `Target` nil).

### 2c. Sign at checksum time — `src/cli/cmd/build_binary.go:569-578`
In the `if t.Checksums` block, after `WriteChecksums` returns `checksumPath`:
**explicit profile only — the `legacy` default does NOT auto-sign blobs** (per locked
back-compat decision). Guard on `t.SigningProfile != ""` (an explicit reference, not the
synthesized default); when set, `sp, _ := config.ResolveSigningProfileForTarget(t,
pc.Config.Signing)`, `plan := sign.Compile(sp)`, `cosign.SignBlob(ctx, …, checksumPath, plan)`,
and `rb.Record(..., Outcome{Type: OutcomeTypeBlobSignature, BlobSignature: &...})`.

### 2d. Release auto-upload — `src/artifact/view.go` + `src/cli/cmd/release_create.go`
Add `BuildBlobSignatureViews(outputs, results)` (mirror `BuildArchiveExecutionViews`
:317-370) yielding `{Path: SignaturePath}`. In `release_create.go:385-422` append a
`releaseAsset{Kind:"signature", AssetPath: sv.SignaturePath}` so it flows into
`manifestAssets` → `allAssets` → the existing `UploadAsset` loop (:543-557) with no
upload-path change. Sort key `(Kind, ArtifactID)` (:428-433) slots it deterministically.

### Commit 2 verification
- Unit: cosign-renderer `SignBlob` with an **ephemeral local cosign keypair** generated in-test
  (`class:key`, tlog off, offline) — assert `SHA256SUMS.sig` is produced and
  `cosign verify-blob` validates it. `BlobSignatureOutcome` round-trips serialize/validate;
  `BuildBlobSignatureViews` yields the sig path; `release_create` includes it in `manifestAssets`.
- Manual: a binary-archive target with `checksums: true` + `signing_profile` produces and
  uploads `SHA256SUMS.sig`; a target without a profile still produces unsigned `SHA256SUMS`.

---

## Critical files

| File | Change |
|---|---|
| `src/config/signing.go` (new) | ✅ `SigningProfile` (`requires` + class blocks + flat assurance keywords) + `legacy` synthesis + validation + resolve |
| `src/config/config.go`, `target.go` | ✅ `Config.Signing` (`signing_profiles`), `TargetConfig.SigningProfile` |
| `src/config/signing.go`, `validate.go` | ✅ finder, resolver, validation (`Find`/`ResolveSigningProfileForTarget`, `ValidateSigningProfiles`, wired into `Validate`) |
| `src/sign/` (new pkg) | ✅ pure IR + compiler: `SignPlan` (assurance requirements), `Compile`, `Enabled`, trust types (no cosign) |
| `src/sign/cosign/` (new pkg) | ✅ cosign **renderer** — `Render(plan,op,env)` (constraint solver), declared `Env`, `resolveKMSURI` shipped; ⏳ executor fns + `signEnv` next (ALL cosign knowledge confined here; no interface/registry) |
| `src/build/docker/sign.go` | **delete** `CosignSign`/`CosignAttest`/`CosignAvailable` (logic moved to `src/sign/cosign`) |
| `src/build/docker/image_engine.go:184-193` | attach resolved profile to `RegistryTarget` |
| `src/build/docker/execute.go:455-462`, `record_outcomes.go:119-171` | `sign.Compile` → `cosign.SignImage/Attest` |
| `src/build/plan.go:76-85` | `RegistryTarget.SigningProfile` |
| `src/artifact/results.go`, `view.go` (C2) | `BlobSignatureOutcome`, blob-sig view |
| `src/cli/cmd/build_binary.go:569-578`, `release_create.go:385-422` (C2) | sign + upload SHA256SUMS.sig |

## Reserved model — Trust Composition / fulfillment (DESIGN ONLY; not implemented in this plan)

The real destination StageFreight is converging toward is not "a signer" but a **trust-fulfillment
system** — *a release should ship on time, acquire the strongest available trust evidence, record
what happened, allow stronger evidence to be attached later, and never silently downgrade trust.*
The signer is one execution backend inside that. This model is **reserved here so the v1 seam is
evaluated against the real destination** — but **none of it is built in Commit 1/2**, and crucially
**none of it changes `SignPlan`**. `SignPlan` stays exactly "one trust requirement, one signing
attempt"; the trust system *composes* plans above it.

The eventual stack (reserved types — design-doc level, no executable behavior now):
```
ReleaseTrustIntent          // desired evidence for a release (e.g. hardware primary + oidc secondary)
      ↓  (compose many profiles → many plans)
TrustExecutionPlan = []SignPlan        // ← the v1 IR is exactly one element of this
      ↓  (execute best-effort; never block publish)
SignatureSet { Primary *Signature; Secondary []Signature; Pending []PendingIntent }
      ↓
ReleaseTrustState   // TrustSatisfied | TrustPartial | TrustPending | TrustFailed
```
- **Layered evidence, not fallback.** Hardware (if present) = PRIMARY; oidc/kms = SECONDARY
  (additive, independent); a missing hardware sig = PENDING (attachable post-facto). A weaker proof
  is never recorded as equivalent to a stronger one — `hardware ≠ oidc ≠ kms`, never interchangeable.
- **Availability decoupled from trust strength.** An execution mode (`best_effort_release`,
  `hardware.timeout`, `allow_secondary`) lets publish proceed while recording intent — **never** in
  `SignPlan`, which carries no timeout/fallback/pending concept.
  - *v1 behavior until then:* `requires: hardware` on a runner without the satisfying device is
    `|D|==0` → a **hard signing error** (loud; never a silent downgrade to oidc/kms). That loud
    failure is correct v1 semantics — decoupling availability from trust strength is exactly what
    this phase reserves, not a v1 gap.

**Why this is correctly deferred:** the genuinely-unresolved question is *not* `SignPlan` — it is
**"what is a release's trust state?"** (Is a hardware-pending release valid/publishable? Is oidc-only
equivalent to hardware? When does Pending become Failed? Can publish occur before Primary? How is
trust state surfaced to verification? Can a sig be attached 3 days later?). These are *release
lifecycle* questions, not signer questions, and (1)+(2) answers **none** of them. The fact that the
whole stack layers on top of an unchanged `SignPlan` is the proof the seam sits in the right place.
**Ship (1)+(2), dogfood on real releases, then design the trust-state model against what evidence
actually needs tracking.**

(**ZITADEL note:** an IdP, not a signer — it maps to the `oidc` class as the `issuer`, never to
`hardware`/`kms`. Even YubiKey→WebAuthn→ZITADEL→OIDC classifies as `oidc` trust, not `hardware`.
The current `oidc.issuer` field already handles it; no schema change.)

## Reserved model — Attested Build Provenance (DESIGN ONLY; separate from signing)

The orthogonal trust layer, and the one that closes the boundary **signing alone cannot**: signing
protects integrity *from signing forward*, but a compromised runner can legitimately sign a malicious
artifact — **build-time compromise** is the residual gap. Build provenance closes it by making the
build itself auditable *after the fact*, turning "we trust CI did the right thing" into "we can detect
abnormal CI behavior." Same separation principle as everything else: **signing answers *who vouches*;
provenance answers *how it was built*** — different concerns, never conflated.

**Foundation already exists** — StageFreight emits SLSA / in-toto provenance
(`src/build/provenance.go`: `ProvenanceStatement`, DSSE envelope). Today it is *coupled* to signing
(DSSE-wrapped + `cosign attest` only when a key is present). The reserved direction:
- **Decouple provenance from signing** — provenance is a first-class build output, generated and
  recorded regardless of whether/how the artifact is signed. Signing then *also* binds trust over the
  provenance statement; it does not gate its existence.
- **Reproducible-build metadata** — record inputs/toolchains so a third party can independently
  re-derive the digest (transport carries verified bytes; reproducibility proves the digest means
  what it claims — see `src/cas`).
- **Isolated / external builder attestation** + **transparency** (Rekor-style log / cross-signed
  release proof) so abnormal builder behavior is detectable post-hoc.

All three trust layers are orthogonal and compose on the **unchanged** v1 seam:
- signing seam (this plan) — execute one trust requirement (`SignPlan → Render`).
- Trust Composition (reserved) — *who vouches*, layered (`SignatureSet`, `ReleaseTrustState`).
- Attested Build Provenance (reserved) — *how it was built*, independently attestable.
Signing simply binds trust over both the artifact digest **and** the provenance statement; neither
reserved layer changes `SignPlan` or the renderer. **Reserved as design; not implemented in Commit 1/2.**

## Operational trust model — self-hosted paths, always-on signing, managed infrastructure (DESIGN; gated)

The Commit 1/2 seam answers *"how is one signature produced."* This part answers the questions that
decide whether signing is **actually adopted**: which trust methods, self-hosted with no 3rd-party
reliance, *always on by default* without a human in the loop, with signatures persisted where the
operator chooses and never leaked into the repo, and documented so a consumer can verify. It is the
frozen target; implementation is gated and sequenced in **O.9**.

### O.1 The split that organizes everything — key-custody vs identity-attested

Two families. Conflating them is what makes signing feel like a toy:

- **Key-custody trust (`key`, `kms`, `hardware`)** — the signature verifies against a **public key**.
  No CA, no transparency service, no 3rd party; offline-capable. This is `--use-signing-config=false
  --tlog-upload=false` (already emitted). cosign-key, Vault/OpenBao, YubiKey/Titan, SoftHSM, TPM all
  live here. **Fully self-hostable today** — the gap is workflow + custody + docs, not cryptography.
- **Identity-attested trust (`oidc` keyless)** — the signature is bound to an OIDC identity by
  **Fulcio** (a CA) and logged in **Rekor** (transparency). Self-hosting means running the Sigstore
  stack (Fulcio + Rekor + CT + a TUF root). ZITADEL/Keycloak/GitLab are *issuers* feeding it, useless
  without the CA. This is the genuinely large project.

Verification stays uniform across both: a consumer pins **a public key** (key-custody) or **an
`(issuer, subject)` identity + the Fulcio root** (keyless). Climbing tiers never breaks consumers.

### O.2 The signing paths and where each stands

| Path | Class | Self-hosted | Reality | Gap to real |
|---|---|---|---|---|
| cosign key/password | key | ✅ | works (proven e2e) | docs |
| Vault / OpenBao (transit) | kms | ✅ | `kms.ref → hashivault://` resolves now | auto-provision + workload-identity binding + docs |
| YubiKey / Titan | hardware | ✅ | renderer emits `--sk`/`--key pkcs11:` | human-gated `sign` command + PIN/touch + hardware validation |
| SoftHSM / network HSM | hardware | ✅ | same `pkcs11:` path | custody standup + docs |
| TPM-bound | hardware | ✅ | same key-custody render | host wiring |
| Private Sigstore (+ ZITADEL/Keycloak/GitLab issuer) | oidc | ✅ ops-heavy | renderer points at public TUF only | deployment endpoint wiring + stand-up guide |
| Public Sigstore (gitlab.com/GH) | oidc | ❌ 3rd party | n/a self-hosted | "cool to have," not the target |

**cosign key + password mechanics:** a keypair is `cosign.key` (private, *always* scrypt+nacl-encrypted,
even empty password) + `cosign.pub`. cosign reads the key via `--key <path|ref>` and decrypts with
`COSIGN_PASSWORD` (now forwarded by `signEnv`). In our model the legacy ref `env:COSIGN_KEY` resolves
to the `--key` value (a path or a `hashivault://` ref). A raw key in CI is the **weakest** class
(exportable) — prefer Vault transit, where the key never leaves the custodian.

**OIDC keyless mechanics:** OIDC token → Fulcio mints a ~10-min cert binding `(issuer, subject)` to an
ephemeral key → sign → log to Rekor → verify against the Fulcio root + expected identity. The issuer is
**GitLab CI ID tokens** (`id_tokens:`, workload identity — same family as GitLab→k8s/Vault auth) for
unattended CI, or **ZITADEL** for human-attributed signing. **Public Fulcio trusts only public issuers
(gitlab.com, GitHub, Google) — never `gitlab.prplanit.com`/ZITADEL** — so self-hosted keyless REQUIRES
a private Sigstore trusting your issuers, with the renderer pointed at *those* URLs (O.4-B).

### O.3 Always-on signing — the default, and why it's honest

The adoption problem: secure options (KMS/YubiKey) cost effort *per build*; the easy option (key in CI
vars) is forge-forever-on-leak; so everyone ships unsigned. The unlock is that **reproducibility is the
safety net** — if builds reproduce and signing events are logged, a forged signature signs a digest
that *won't reproduce from source*, so misuse is **detectable**. An always-on key needn't be unstealable;
its *misuse* must be **detectable**. Trust and reproducibility are one feature, not two.

**Default behavior (gated target):** on first build with signing enabled (**the default**), StageFreight
**auto-provisions** a persistent signing identity into a custodian in the durable state-dir (O.6),
**auto-publishes** the public key once, and signs every build thereafter — *no secret the operator ever
touches*. The custodian is a **ladder with identical consumer verification**:

| Tier | Custodian | Non-exportable | Effort | Role |
|---|---|---|---|---|
| 0 (default) | auto-gen software key in the state-dir | no — **misuse detectable** via repro + transparency | zero | "always-on for everyone" floor |
| 1 | SoftHSM / signing-agent (PKCS#11) | API-level | near-zero (ships in runner) | better floor, turnkey |
| 2 (recommended) | **Vault/OpenBao transit, bound to the runner's existing OIDC identity** | yes, server-side | low (reuses existing OIDC→Vault plumbing) | the real tier |
| 3 | TPM-bound | hardware | host setup | if runners have a TPM |
| 4 | KMS / YubiKey | hardware | per-release human/setup | official releases |

Friction killers, all StageFreight's job: **auto-provision (never copy-paste a key); auto-publish the
anchor; uniform `cosign verify` across the ladder; one-line upgrade** (`custodian: vault` swaps Tier 0→2,
zero consumer impact); **bounded blast radius** (non-exportable custody = sign-during-window not
steal-forever; transparency + reproducibility make even that detectable; rotation bounds lifetime).

**Honest limit:** Tier 0's software key is readable by a root-level runner compromise — the defense is
*detectability, not unstealability*, and the ramp to non-exportable (Tier 2) is one config line reusing
infra you already run. The "same fingerprint" is the **published public key** (stable across releases;
rotation, when added, publishes a signed chain so old signatures still verify — the identity is stable,
the key may rotate under it, mirroring the alias→identity rule in the release-channels design).

### O.4 The architectural unlocks (shared by every path)

**(A) A separable `stagefreight sign` Publish command.** Inline signing-during-build (shipped) is right
for *unattended* classes (key/kms in CI). YubiKey is *human-in-the-loop* — build in CI, sign later with a
finger on the key — which demands a **detached action over already-built artifacts**. `stagefreight sign
<release|ref>` loads the artifacts (SHA256SUMS, image digests from the manifest/content-store), runs the
configured profile (prompting for touch/PIN on hardware, silent for key/kms/oidc), and attaches/uploads
the signatures. One command unlocks YubiKey, hosts the future Trust-Composition "attach stronger evidence
later," and works for every class. Publish owns this orchestration (foundational invariant 4).

**(B) Deployment-wiring config for endpoints.** Profiles stay abstract (`requires: oidc`, never URLs).
Fulcio/Rekor/issuer URLs and `hashivault://` KMS URIs are **deployment wiring**, resolved at render time
— extending the existing `resolveKMSURI` (`SF_SIGN_KMS_<REF>`) pattern with
`SF_SIGN_SIGSTORE_{FULCIO,REKOR,ISSUER,TRUSTED_ROOT}` threaded into the renderer's transparency path
(`--fulcio-url --rekor-url --oidc-issuer --trusted-root`). Keeps profiles portable while an operator says
"…via *my* Fulcio." (Env for secrets/URLs; a structured block only if it grows structure.)

**(C) Human-interactive signing — your OIDC platform as identity, or as a convenience gate.** `stagefreight
sign` (A) is the home for *interactive* signing where a human authenticates to the org's OIDC
(ZITADEL/Keycloak) at sign time. Operators **will** ask for this; StageFreight enables it and labels its
trust honestly — the framework serves the operator's chosen model, it does not police it. Two distinct
postures:
- **(C1) OIDC-as-identity (human keyless).** Interactive issuer login (browser / device-flow) → token →
  Fulcio → cert bound to the *human's* `(issuer, subject)` → sign. The signature is **attributed to the
  human**. Strongest attribution; rides the private-Sigstore endpoints (B); `requires: oidc`.
- **(C2) OIDC-as-authorization (gate to a custodial key).** The human authenticates via OIDC only to be
  *authorized* to invoke a custodial signer (e.g. Vault's OIDC auth method → a transit sign). The
  signature's identity is the **custodial key** (`requires: key|kms|hardware`), **not** the human;
  per-human accountability lives in the OIDC/Vault audit log, not the signature. Convenient (SSO, no
  per-human key, no Fulcio) but **weaker attribution** — the honest "not ideal, but who-are-we-to-police-it"
  tier the operator opts into knowingly.

The model boundary this introduces — **authorization is orthogonal to trust class.** `requires` still
names *who cryptographically vouches* (the key/identity); a new optional **`authorization`** facet (e.g.
`authorization: oidc`) names *who is allowed to invoke the signer* — a deployment/access concern, never
the trust anchor. (C1) puts OIDC in `requires` (it *is* the identity); (C2) puts OIDC in `authorization`
(it gates a key-custody class). Conflating the two — treating an authz gate as the trust identity — is
precisely the soundness error invariants 6/9 forbid; keeping them separate is what lets StageFreight
offer (C2)'s convenience without lying about what the signature attests.

### O.5 Managed signing infrastructure — the durable state-dir + tiered standup

Signing material is **durable trust state**, categorically different from ephemeral build state. Today
everything StageFreight writes is under `/stagefreight` (`.stagefreight/dist` cleared, `.stagefreight/
objects` clearable). Trust roots must not live there.

- **Tier 0 — the state-dir primitive:** a runner-config `signing.state_dir` (or named volume
  `stagefreight-signing`) defaulting **outside** the build mount (e.g. `/var/lib/stagefreight/signing`),
  the home for all persistent signing material — keys, Vault data, Sigstore state, trusted roots, agent
  sockets. Solves "don't clear it by accident" by construction; the natural per-operator backup unit.
  (Shape: named volume with an operator-overridable host path.)
- **Tier 1 — custodial standup** (Vault/OpenBao, key-agent sidecar, `pcscd`/PKCS#11, SoftHSM-for-dev)
  via runner compose against the state-dir. Collocation with the runner is a pragmatic convenience: the
  key material is operator-held, the build talks to the custodian over a socket, the key never enters
  build scratch.
- **Tier 2 — CA/transparency (private Sigstore, or simpler BYO-PKI: static CA, no tlog).**

**The one trust line:** the build executor must never *become* the trust root (invariant 8). A CA that
runs *on the build runner* means a compromised runner can forge any signature. So Sigstore wants a
**separate deployment** (the dungeon/k8s, runner just points at it); a runner-collocated Sigstore is a
**documented dev/single-host convenience**, never the default for real releases. And **production
trust-root key generation is a deliberate ceremony, not an automatic side effect** — auto-provisioning a
*dev* root is fine; silently generating the root that anchors real release trust is not.

StageFreight **owns the state-dir primitive** and **ships standup scaffolding** (compose/k8s fragments +
renderer wiring) as **opt-in**; the **recommended path stays "point at infra you run."** Service
lifecycles (upgrade, backup, root ceremony) remain the operator's.

### O.6 Persistence — where signatures live, operator-chosen, backed up, never in the repo

`.stagefreight/*` is gitignored, so signatures **cannot** land in the repo tree by construction —
they must flow to a publish destination. By family:

- **Image signatures — solved + operator-chosen.** cosign attaches the sig to the **registry** as an OCI
  referrer (`sha256-<digest>.sig`), beside the image; the operator already picks the registry (your
  Harbor/mirror topology); backup = the registry's backup. `release_create` already discovers it
  (`registry.DiscoverAllArtifacts` → `ImageRow.Signature`, `release/notes.go`) and surfaces it.
- **Blob signatures (`SHA256SUMS.sig`) — the stranded gap.** Written to `DistDir` (gitignored, ephemeral),
  recorded as a `blob_signature` *outcome* — but `release_create` uploads assets **only from the
  manifests** (`release_create.go` ~386-442), so a `.sig` in `DistDir` is *ignored* and never reaches the
  release. **Fix (O.9-#1):** a `BuildBlobSignatureViews` → `releaseAsset{Kind:"signature"}` so it flows
  into the existing `UploadAsset` loop. Durable home = the **forge release** (operator storage,
  forge-backed-up, outside the repo).
- **Operator-chosen storage beyond the forge (e.g. your Ceph RGW bucket):** the `cas.Store` interface
  already reserves the non-OCI/object-store quadrant (`cas/cas.go` ~68-72) — *designed, not built*. A
  future object-store `Store` backend persists artifacts+sigs to an operator bucket with RGW backup.

### O.7 Verification surface — the stable anchor + documentation

Separate the **per-release signature** (different every release — signs different bytes) from the
**stable verification anchor** (the public key fingerprint, or `(issuer, subject)` — invariant 8, pinned
once to verify all releases). Surface both, in the right places:

- **Stable anchor → a durable `SECURITY.md` / verification doc** (the pubkey/identity + the
  `cosign verify-blob --key <pinned> --signature SHA256SUMS.sig SHA256SUMS` recipe). Not generated today;
  hand-authored or a small generator emitting the recipe from the configured profiles.
- **Per-release → a "Verification" section in release notes** (`release/notes.go:renderNotes`).
  `ImageRow.Signature` already surfaces image sigs; **`BinaryRow` lacks a signature field** and there is
  **no Verification section** — both to add, referencing the pinned anchor.
- **Config reference auto-docs:** `signing_profiles` is a config section, so `stagefreight docs generate`
  (struct-reflection + `internal/docsgen` override map) auto-documents it once an override entry is added;
  new signing CLI flags auto-doc via Cobra.
- **Badge:** `.stagefreight/badges/` is the one non-gitignored `.stagefreight` path — a "signed" badge can
  be emitted there (separate from docsgen).

### O.8 What is true today vs the target

- **Dev-build images:** real now — signed on push, stored+backed-up in the registry, surfaced in notes —
  once `COSIGN_KEY`/`COSIGN_PASSWORD` (or Vault transit) are set.
- **Releases:** `SHA256SUMS.sig` is *produced but stranded* (not uploaded, not in notes, no verify doc).
- **Always-on / self-hosted custody / `stagefreight sign` / private Sigstore / object-store:** designed
  here, not built.

### O.9 Gap-closure roadmap (ordered; each gated on sign-off)

1. **Blob-signature asset view** — upload `SHA256SUMS.sig` to the forge release. *Small, fully testable
   now; the difference between "we sign it" and "you can get the signature."*
2. **Release-notes Verification section + `BinaryRow` signature surfacing**; a `SECURITY.md`/verification
   doc with the pinned anchor + recipe; `signing_profiles` docsgen override.
3. **The durable state-dir primitive** (O.5 Tier 0) — outside `/stagefreight`, the home for persistent
   identity.
4. **Always-on default: auto-provision + auto-publish** the Tier-0 software identity — flips signing to
   *on by default* with zero friction (the highest-leverage item for adoption).
5. **`stagefreight sign` command** (O.4-A) — unlocks YubiKey (human-gated) and covers key/kms/oidc;
   *built by us, hardware-validated by the operator.*
6. **Vault-transit-via-workload-identity** (Tier 2) — auto-provision + OIDC auth onto the existing `kms`
   class; the recommended real tier.
7. **Deployment endpoint wiring** (O.4-B, `SF_SIGN_SIGSTORE_*`) + renderer flags — turns on private
   Sigstore once the operator stands one up.
8. **Human-interactive OIDC signing** (O.4-C) — interactive `stagefreight sign` modes + the `authorization`
   profile facet: **C1** (OIDC-as-identity, human keyless via #7) and **C2** (OIDC-as-authorization gate to
   a custodial key, e.g. Vault OIDC auth → transit). Built on #5+#6+#7; the operator's-own-IdP convenience
   tier, honestly labeled.
9. **Transparency hook** (self-hosted Rekor or a minimal append-only signed log) — turns "detectable in
   principle" into "detectable in practice," pairing with reproducibility.
10. **Object-store `cas.Store` backend** — sigs/artifacts in an operator RGW bucket (the fullest
    "operator chooses storage + backs it up").
11. **Private Sigstore stand-up scaffolding** (separate deployment) + operator guide — mostly ops;
    StageFreight side largely done by #7.

Constraints: YubiKey (#5) and Sigstore (#7/#8/#11) cannot be validated in a container — built correctly,
confirmed by the operator on real hardware/infra. Private Sigstore is ~80% ops, ~20% StageFreight code.

## Design framing & watch-points

This is a **constrained trust algebra**, not a signing feature: Profile = theorem, `SignPlan` =
normalized proof obligations, `Render` = witness construction, `Env` = the model of available
witnesses. It decomposes into three layers — (1) **identity semantics** (`profile → Compile → SignPlan`),
(2) **witness resolution** (`Env → Render` selection), (3) **execution** (cosign). `Render` is therefore
not "just execution": it is the **identity-realization boundary — the one place identity meets reality**,
which is exactly why the identity invariants (6–12) concentrate there and why it stays pure + total +
fully specified. In this domain, "practical flexibility" suggestions are usually soundness-corruption
attempts — pushback must be **invariant-based, not solution-based**. Three falsification passes found
no logical break (rotation / CI fanout / partial-hardware / staged-rollout). The deep result: the model
is sound where identity is **cryptographic** (mathematical, derivable from the key) and fragile only
where it would treat **infrastructure identity** (OIDC claims, capability labels, env graphs) as
reducible to that — they do **not** collapse, so the model spans **two identity domains** (inv. 6) and
never pretends uniformity. Cryptographic principals are *derived* (fingerprint), so multi-region KMS /
HSM-pool "false ambiguity" is `|D|==1` by math, not Env-honesty. Claim identity (OIDC) is a separate
system; capability labels are honest-until-attested (inv. 7); the principal→authority binding is the
irreducible, *explicit* config-trust root (inv. 8).

Watch-points (monitoring, **not** change requests — they confirm the boundary):
- **Rotation overlap** is invariant-*confirming*: it forces explicit env narrowing, never a silent pick.
- **`Render` is the identity-realization boundary — where future bugs will concentrate.** Contained
  via the pure internal `select`/`emit` split and guarded by the **identity-invariant suite (6–12)**,
  which is the enforceable expression of "Env constrains mechanism, never identity." Watch for future
  provider-specific branching creeping in.
- **The cryptographic/infrastructure identity seam is the critical boundary.** Crypto classes derive
  the principal (mathematical, inv. 6) — robust. The residual risk lives entirely in the infrastructure
  domain: OIDC claim verification (reserved verification phase), capability **attestation** (inv. 7,
  reserved), and `Env` never becoming a *profile* channel. The next-level test is **adversarial `Env`
  construction**; for crypto classes derived-fingerprint grouping defeats it, for OIDC/capabilities the
  defense is attestation/claim-verification, which v1 explicitly does **not** yet do — so v1 must not
  overstate the trust of an un-attested `hardware` profile or an unverified `oidc` identity.

## Build/commit discipline
All build/test in the dogfood container (`golang:1.26-alpine` for tests,
`prplanit/stagefreight:latest-dev` for `stagefreight commit`). Commit scope `feat(sign)`; the seam
shipped incrementally (config+IR core `9da9deb`, renderer `6e539d2`) rather than as one commit. Use
`stagefreight commit`/`stagefreight push` (the guarded host binary autostashes uncommitted work);
the dev image push to dockerhub remains separately blocked on `docker login`.
