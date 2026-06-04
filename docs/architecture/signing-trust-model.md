# StageFreight Signing — Trust Model & Implementation Plan

> **Status: living design document.** This is the durable, iterable home for StageFreight's
> signing architecture. It has been pressure-tested across many rounds (architecture, security
> boundary, operational reality, three adversarial falsification passes). Iterate it *here*.
>
> **Implementation (Commit 1 seam, Commit 2 SHA256SUMS) is gated** pending explicit design
> sign-off — this document existing is not a trigger to start building.

## Context

StageFreight already signs image digests with cosign, but the signer is hardcoded
to `--key <path>` (`src/build/docker/sign.go:27-31`), `--tlog-upload=false`, and an
implicit key discovered from `COSIGN_KEY` / `.stagefreight/cosign.key`. There is no
way to select a signing *method* (hardware/YubiKey, keyless/OIDC, KMS) and no way to
sign the release bundle (`SHA256SUMS` is produced unsigned at `archive.go:160-182`).

This delivers the two foundational pieces of the agreed "layered" signing model:

1. **A signer seam** — a top-level `signing:` block of named policies (`requires` =
   trust class *intent*: `key` | `oidc` | `kms` | `hardware`), referenced per-target by
   `signing_policy: <id>` (the same reference-by-id pattern as `registry:` →
   `FindRegistryByID`). A policy compiles to a neutral `SignPlan`, rendered to cosign — no
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
scalar-or-list and the policy is a named object, so all are additive.

## Locked decisions

- **`requires` names the trust class — that is all policy carries.** A policy declares the
  trust *requirement*; it never names a device, vendor, transport, or provider. Classes:
  `key`, `oidc`, `kms`, `hardware`. `requires` is scalar-or-list (v1 enforces exactly one;
  multi-trust `[hardware, oidc]` parses but v1-errors as "deferred"). Aliases `keyless→oidc`,
  `yubikey→hardware` normalized in `Normalize`; `yubikey`/`fido2`/`vault`/`aws` are rejected
  as classes — they are machinery.
  ```yaml
  signing:
    - id: ci-oidc
      requires: oidc                                  # identity-backed ephemeral signing
      oidc: { issuer: "...", identity: "..." }        # expected signer identity (optional)
    - id: maintainer
      requires: hardware                              # non-exportable key + physical presence
      hardware:
        capabilities: [non_exportable_key, physical_presence]   # required trust PROPERTIES (closed enum)
        attestation: required
    - id: org-automation
      requires: kms                                   # managed/remote key custody
      kms: { ref: release-signing-key }               # LOGICAL ref — bound to a URI at render time
    - id: release-key
      requires: key
      key: { ref: "env:COSIGN_KEY" }                  # "path" or "env:VAR"
      tlog: false                                      # optional override (any class)
  targets:
    - { id: dockerhub-stable, kind: registry, build: stagefreight, signing_policy: maintainer }
  ```
  **No machinery in policy.** The hardware *transport* (FIDO2 `--sk` vs PKCS#11) is selected at
  render time from what the environment offers, gated by the required `capabilities` — never
  named in policy. KMS uses a **logical ref** bound to a URI at render time (`resolveKMSURI`, 1e),
  so the provider/URI lives in deployment wiring, not the trust policy.
- **Legacy is a default policy, not a parallel code path.** `Normalize` synthesizes an implicit
  `id: legacy, requires: key, key.ref: env:COSIGN_KEY`. A target with no `signing_policy`
  compiles to the `legacy` plan. ONE path — `Compile` over a policy — never a separate legacy
  branch in `sign.go`. The `legacy` plan is `Enabled` iff the key actually resolves
  (`ResolveCosignKey() != ""`), preserving today's no-key-no-signing.
- **Back-compat = implicit image signing ONLY.** A target with no `signing_policy`
  keeps today's implicit key-signing for images, byte-identical. The implicit key does
  **not** auto-sign `SHA256SUMS` (or SBOM, hardware, OIDC) — those require an explicit
  policy. "Existing configs produce identical outputs unless explicitly changed."
- **Sequencing = seam first (Commit 1), SHA256SUMS second (Commit 2).** The seam is
  the architecture; blob signing is a consumer. Prove the abstraction with an
  unchanged dogfood build before adding the consumer.
- **Compiler model, not a backend system. cosign is a pure output target.** The seam is a
  deterministic lowering through a pure neutral IR — `SigningPolicy → Compile → SignPlan → render
  → cosign CLI` — **not** a `Backend` interface + registry. A registry/interface would be a second
  resolution graph that mirrors cosign anyway (the abstraction-inversion trap) and violates
  single-source-of-truth. The stable thing is the policy model + `SignPlan` IR (data); cosign is a
  renderer at the edge. No `ResolveBackend`, no `KMSResolver` interface, no plugin graph. A future
  native signer (Vault-Transit, PKCS#11, sigstore-go) is just *another renderer of `SignPlan`*,
  added when it exists — YAGNI until then.
- **`SignPlan` is *requirements* ("what must be true"), never cosign mechanism.** It carries
  required trust properties / references — never `--sk`, `--key`, URI schemes, or flag choices.
  The renderer does **capability satisfaction**, not "cosign mode selection": given the plan's
  requirements and a pure `Env` capability struct (what the runtime offers — KMS/FIDO2/PKCS#11),
  it emits an invocation that satisfies them. No URI or flag concept exists above the renderer.
- **Correctness split — the one principled boundary.** `Compile` owns *all* policy-level
  validation (class coherence, capability enums, ref presence) and is total over valid config —
  static, env-independent. `Render` owns *only* env-satisfiability (Compile can't see whether a
  device is present) and must be **deterministic and loud**: hard-error when the `Env` can't
  satisfy the plan. No silent fallback selection.
- **Package placement:** `src/sign` (pure: trust types, `SignPlan`, `Compile`; imports `config`,
  `toolchain`, **never cosign**), `src/sign/cosign` (the cosign **renderer** — `Render(plan, op,
  env)` + declared `Env` + thin executor; the only cosign-aware package). `src/build/docker` and
  `src/cli/cmd` compile a `SignPlan` via `src/sign` and hand it to `src/sign/cosign`.
  (`src/security/verify.go`'s separate cosign-verify use is out of scope.)
- **tlog default:** off for `key`/`kms`/`hardware`, on for `oidc`; `tlog:` overrides.

## Enforced invariants (each is a test target, not just prose)

1. **No *device* ambiguity in render selection.** `Render` resolves over distinct trust principals
   (signing devices/keys), not transports. `|distinct devices satisfying the plan|`: `0 → error`,
   `1 → use`, `>1 → error` (genuinely different keys — a trust ambiguity that would silently drift
   and break "which key signed this?"). A single device reachable by multiple *transports* (same key
   via FIDO2 + PKCS#11 — identical trust) is resolved by a **deterministic renderer-internal**
   transport preference: trust-neutral, never an error, and never a policy field. `Env` is
   *declared*, not probed. Tests: two distinct devices satisfying the plan → error; one device via
   two transports → deterministic pick (no error).
   - **This does not fight CI fleets.** `Env` is *per node*, and devices group by **key identity**.
     A 100-node fleet where every node holds the *same* key is `|D|==1` per node — redundancy is
     fine, not ambiguity. The genuine `|D|>1` case is two *different* keys on one node — most often
     **key-rotation overlap** — which the env must narrow *explicitly* (not a silent pick), which is
     the whole point: during rotation, "old key vs new key signed this" must stay answerable.
2. **`capabilities` are hardware-class-only.** Invalid on any other class (1c). Test: `capabilities`
   under a `kms`/`oidc`/`key` policy → validation error.
3. **KMS is pure string substitution.** `resolveKMSURI` is `ref → env → URI` verbatim; no provider
   parsing/registry/semantics in core. Test: core packages contain no `vault`/`awskms`/`gcpkms`
   string; only the env value carries a scheme.
4. **`Compile` is pure and cannot fail.** Returns `SignPlan`, no error path. *All* validation is
   upstream in `config.Validate` (runs at audition). The renderer is the only runtime failure
   surface. Test: `Compile` over every validated policy never panics/errors; config validity is
   never a function of the runtime environment.
5. **Legacy is just another policy.** No special-case branch anywhere — the synthesized `legacy`
   policy flows through `Compile`/`Render` identically. Test: grep shows no legacy-specific code
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
7. **Capabilities are labels until attested.** `non_exportable_key`/`physical_presence` are
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
    *policy + attestation*, never from *which transport was chosen*. Transport quality is not trust
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
  into policy).
- **Capabilities are pre-attestation *constraints* in v1**, not verified facts — `Render` uses them
  as declarative filters; they become post-attestation *assertions* only in the reserved attestation
  phase (inv. 7). Future composition must never conflate the two meanings.
- **`config.Validate` is the single, deliberate semantic-correctness gate** Compile depends on — a
  known critical dependency by design (the alternative is the drift-prone double-validation we
  rejected). Its completeness is therefore itself a first-class test target, not a neutral fact.

---

## Commit 1 — The signer seam (refactor; default key path stays byte-identical)

### 1a. Config schema — `src/config/signing.go` (new)
Policy is expressed in trust vocabulary — **no cosign URIs, no devices, no machinery**.
`SigningPolicyConfig{ ID string; Requires StringOrList; Key KeyTrust; OIDC OIDCTrust;
KMS KMSTrust; Hardware HardwareTrust; TLog *bool }`:
- `KeyTrust{ Ref }` — `"path"` or `"env:VAR"`.
- `OIDCTrust{ Issuer, Identity }` — expected signer identity, both optional.
- `KMSTrust{ Ref }` — a **logical** key ref (e.g. `"release-signing-key"`), bound to a concrete
  URI at render time (`resolveKMSURI`, 1e). **Not** a cosign URI — policy stays portable across
  providers.
- `HardwareTrust{ Capabilities []Capability; Attestation bool }` — `Capabilities` is a
  **closed enum** (`non_exportable_key`, `physical_presence`, …) describing required trust
  *properties*, not devices. The renderer (1e) selects a transport satisfying them.

`Requires` parses scalar-or-list (custom `UnmarshalYAML`, scalar coercion). Add
`validTrustClasses = {"key","oidc","kms","hardware"}`, `validCapabilities`, and alias
normalization (`keyless→oidc`, `yubikey→hardware`). `Normalize` synthesizes the implicit
`legacy` default policy. Mirror the flat style of `RegistryConfig` (`forges.go:54-60`).

### 1b. Wire into config graph
- `Config.Signing []SigningPolicyConfig` — `src/config/config.go` (near `Targets`, ~:50).
- `TargetConfig.SigningPolicy string yaml:"signing_policy,omitempty"` — `src/config/target.go`
  (beside `Build`/`Registry`/`Mirror`).
- `FindSigningPolicyByID` — `src/config/forges.go` (beside `FindRegistryByID` :256-264).
- `ResolveSigningPolicyForTarget(t, policies) (*ResolvedSigningPolicy, error)` —
  `src/config/resolve.go` (mirror `ResolveRegistryForTarget` :70-92). Returns the referenced
  policy; the synthesized `legacy` policy when `t.SigningPolicy == ""` (single path — never
  nil for a signable kind); error `target %s: signing_policy %q not found` when set-but-unknown.
  `ResolvedSigningPolicy` is the flattened, vars-resolved view consumed by execution.
- Alias normalization in `Normalize` (`config.go` ~:119-163).

### 1c. Validation — `src/config/validate.go` (the single validation layer)
All signing validation lives here — `src/config` owns the policy structs, and `src/sign` imports
`config` (not vice-versa), so this is the only cycle-free home. `Compile` (1d) is then a **total
transform over validated config** and never re-validates (no error path). Rules, mirroring
build-ref validation: collect `signingPolicyIDs` (empty/non-identifier/duplicate `id`); a `requires`
value outside `validTrustClasses` (reject machinery names `yubikey`/`fido2`/`vault`/`aws`);
**`len(requires) > 1` → "multi-trust composition not yet supported"** (deferred, reserved);
class/field coherence (a nested block for a class not in `requires`; empty `kms.ref`; a
`hardware.capabilities` value outside `validCapabilities`; **`capabilities` present when class ≠
hardware → error** — capabilities are a hardware-class-only refinement, invariant 2). Target loop (~:250): unknown
`signing_policy` ref; `validateTarget` (:509) restricts the field to `kind: registry` and
`kind: binary-archive`. The synthesized `legacy` policy uses a reserved id, exempt from collision
checks. This runs at audition (fail early), keeping correctness static and upfront — never in the
renderer.

### 1d. The IR + compiler — `src/sign` (new package; pure, **never imports cosign**)
The stable model. Imports `config`/`toolchain` only.
- Neutral types: `Class` (`key|oidc|kms|hardware`), `Capability` (closed enum), `Op`
  (`SignImage|Attest|SignBlob`).
- **The IR — pure *requirements*, no mechanism. The insulation boundary as data:**
  ```go
  type SignPlan struct {
      // --- Requirements (what must be true) ---
      Class        Class        // trust class
      KeyRef       string       // key class: "path" or env var name (a reference, not a mechanism)
      KMSRef       string       // logical kms ref (bound to a URI only at render time)
      OIDCIssuer   string       // identity requirements
      OIDCIdentity string
      Requires     []Capability // required trust PROPERTIES (e.g. non_exportable_key, physical_presence)
      // --- Execution modifiers (not requirements; carry no policy logic) ---
      Attestation  bool         // also emit a provenance attestation
      TLog         bool         // upload to transparency log
  }
  ```
  No `--sk`, `--key`, URI, or flag concept here — those are the renderer's vocabulary. The two
  groups are kept distinct so nobody later encodes policy logic into the execution-modifier flags.
- `Compile(*config.ResolvedSigningPolicy) SignPlan` — deterministic lowering, config →
  requirements; **total over validated config** (config.Validate, 1c, is the validation layer —
  Compile does not re-validate). `SignPlan` carries **logical refs** (`KMSRef`, `KeyRef` as a var
  name) — *not* resolved URIs/keys; ref resolution is deliberately Render-time, so "Compile is pure"
  means pure over the *logical policy*, never a claim that the resolved execution is static. The
  `legacy` default policy compiles like any other (single path).
- `Enabled(p SignPlan) bool` — `class:key` with `ref: env:COSIGN_KEY` is enabled iff the key
  resolves; preserves today's no-key-no-signing.
- `SignOptions{ MultiArch bool; PredicatePath string }`; `SignatureResult{ SignatureRef,
  AttestationRef, SignaturePath string }` — plain data passed to / returned from the renderer.

### 1e. The cosign renderer — `src/sign/cosign` (new package; the ONLY cosign-aware code)
A **capability-satisfaction emitter**, not a mode selector. **No interface, no registry.**
- `type Env struct { KMS []Provider; FIDO2 []Device; PKCS11 []Slot; … }` — a **declared** capability
  graph (what witnesses the deployment has *explicitly enumerated*), built from explicit env/config
  — **not auto-discovery**. Declaring rather than probing prevents accidental implicit hardware
  adoption (plug in a key → signing behavior silently changes). **Every witness declares an explicit
  `Principal`** — a stable trust-principal identity (public-key fingerprint; for OIDC the
  `(issuer, subject)` pair). This is the model's single load-bearing assumption made **explicit and
  represented in data**: identity equivalence is **declared, never inferred from transport/endpoint
  shape**. Plain data, not an abstraction.
- `Render(p sign.SignPlan, op sign.Op, env Env) (args, runEnv []string, err error)` — **pure**
  given `(plan, op, env)`; a **constraint solver over distinct trust principals, not witnesses**.
  Filter `env` to the plan's required class, then let `D` = the distinct **`Principal`s** among the
  satisfying witnesses — for the cryptographic classes (`key`/`kms`/`hardware`) grouped by the
  **derived public-key fingerprint** (math, not Env-declared label; see inv. 6), for `oidc` by the
  `(issuer, subject)` claim — counted across *all* endpoints/transports/slots: `|D|==0 → error`
  (unsatisfiable); `|D|>1 → error` (genuinely different keys — e.g. YubiKey #1 vs #2 — a trust
  ambiguity, never a silent pick); `|D|==1 → use it`. **One `Principal` reached many ways is
  `|D|==1`** — so multi-region KMS (one key ARN, two regions), an HSM pool (one key, many slots),
  and one key via FIDO2 **and** PKCS#11 all correctly resolve to a single principal, *not* a false
  ambiguity. When that one principal has several reaches, a **deterministic renderer-internal
  preference** picks the endpoint/transport — trust-neutral, never an error, never a policy field.
  (OIDC: `(issuer, subject)`, so two *different* issuers asserting the same subject are two distinct
  principals → correctly `|D|>1` → error; cross-issuer equivalence is a deliberate *verification*
  decision, never a silent collapse.)
- `Render` MAY be two pure internal functions — `select` (plan + env → the one satisfying mechanism)
  then `emit` (mechanism → cosign args) — to avoid a single dense function. This is **internal
  structure, still no interface/registry/resolution-graph**: both are pure, both live in
  `src/sign/cosign`, neither is a domain abstraction. (Guards against the "god-function" critique
  without reintroducing the backend graph.) Flag mapping:
  - key → `--key <path> --tlog-upload=false`
  - oidc → no `--key`, `--tlog-upload=true`, issuer/identity via env
  - kms → requires `env.SupportsKMS`; `--key <resolveKMSURI(p.KMSRef)>`, tlog per plan
  - hardware → the mechanisms in `env` that satisfy `p.Requires`. **Render must never *choose*
    among several valid mechanisms** (invariant 1): require **exactly one** satisfiable transport →
    `--sk` (FIDO2) or `--key <pkcs11>`; error `no satisfiable transport` if zero, error `ambiguous
    satisfiable transport` if more than one (the deployment env narrows it).
  - `--upload=true` for sign/attest, omitted for sign-blob.
- `resolveKMSURI(ref) string` — **pure string substitution only** (invariant 3): `ref` →
  `$SF_SIGN_KMS_<REF>` → URI, verbatim. No provider semantics, no parsing, no registry — StageFreight
  core never knows vault/aws/gcp/azure. The concrete URI never enters policy or `SignPlan`.
- Executor functions (concrete, not interface methods): `SignImage(ctx, rootDir, desired,
  digestRef string, p sign.SignPlan, o sign.SignOptions)`, `Attest(...)`, `SignBlob(ctx, …,
  blobPath string, p sign.SignPlan)`, `Available(env)`. Each calls `Render`, then execs cosign with
  `toolchain.Resolve("cosign", …)`, `COSIGN_YES=1`, and `signEnv(p)` (hermetic `CleanEnv` for key;
  OIDC/KMS/PKCS#11 env forwarded per class). Hardware is interactive (device + presence/PIN) —
  **wired but not unattended-CI-runnable** (commented).

### 1f. Refactor call sites onto the compiler — `src/build/docker`
The cosign functions in `docker/sign.go` (`CosignSign`/`CosignAttest`/`CosignAvailable`, :15-93)
are **deleted**; logic moves to `src/sign/cosign`. `recordAttestationOutcomeIfConfigured`
(`record_outcomes.go`) does `plan := sign.Compile(policy)` (config already validated at audition)
then, if `sign.Enabled(plan)`, `cosign.SignImage(ctx, …, plan, opts)` / `cosign.Attest(...)`. The
call site holds a neutral `SignPlan` and invokes the cosign renderer at the edge — the policy→plan
half is fully cosign-free.
`ResolveCosignKey` and `signEnv` move to `src/sign/cosign`; `toolchain.CleanEnv` stays the hermetic
primitive the renderer reuses.

### 1g. Threading (single path via the `legacy` default policy)
- `ResolveSigningPolicyForTarget(t, cfg.Signing)` returns the referenced policy, or the
  synthesized `legacy` policy when `t.SigningPolicy == ""` — **never nil for a signable kind**.
  One resolution path; no separate legacy branch.
- `RegistryTarget.SigningPolicy *config.ResolvedSigningPolicy` — `src/build/plan.go:76-85`.
- Populate at the lowering seam `src/build/docker/image_engine.go:184-193`: after
  `ResolveRegistryForTarget`, call `ResolveSigningPolicyForTarget(t, cfg.Signing)` and set
  `SigningPolicy: sp` (propagate error like :152-154).
- `src/build/docker/execute.go:455-462`: the build-scoped `cosignKey` string is gone; the
  per-target loop passes `reg.SigningPolicy` (always populated — `legacy` when unset) into the
  recorder, which compiles it via `sign.Compile`.
- `recordAttestationOutcomeIfConfigured` (`record_outcomes.go:119-171`): replace
  `cosignKey string` with `policy *config.ResolvedSigningPolicy`; compute
  `plan := sign.Compile(policy)`; guard `if !sign.Enabled(plan) || digest == ""`; then
  `cosign.SignImage(...plan...)` / `cosign.Attest(...plan...)`.
- **Back-compat guarantee:** no `signing:` and no `signing_policy` ⇒ `legacy` policy ⇒
  `class:key`/`env:COSIGN_KEY` ⇒ `Render` emits the *identical* `--key <path>
  --tlog-upload=false --upload=true` set as today. Byte-for-byte unchanged.

### Commit 1 verification
- Unit: `sign.Compile` (config policy → `SignPlan` requirements, every class, legacy default —
  total transform); config validation (1c) policy rules; `src/sign/cosign` `Render` table tests
  (every class × op, tlog polarity, override) **including the unsatisfiable-`Env` hard-error** (e.g.
  hardware required but `Env` offers neither FIDO2 nor PKCS#11; kms required but `!SupportsKMS`);
  `resolveKMSURI` env lookup; config validation (id uniqueness, bad `requires`/machinery-name
  rejection, multi-trust deferred-error, capability/coherence, ref existence, kind restriction);
  `ResolveSigningPolicyForTarget` found / not-found / legacy-default; `image_engine` populates
  `RegistryTarget.SigningPolicy`; `legacy` plan renders to the same args as today.
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
**explicit policy only — the `legacy` default does NOT auto-sign blobs** (per locked
back-compat decision). Guard on `t.SigningPolicy != ""` (an explicit reference, not the
synthesized default); when set, `sp, _ := config.ResolveSigningPolicyForTarget(t,
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
- Manual: a binary-archive target with `checksums: true` + `signing_policy` produces and
  uploads `SHA256SUMS.sig`; a target without a policy still produces unsigned `SHA256SUMS`.

---

## Critical files

| File | Change |
|---|---|
| `src/config/signing.go` (new) | `SigningPolicyConfig` (`requires` + trust-prop blocks) + `legacy` default synthesis + valid set |
| `src/config/config.go`, `target.go` | `Config.Signing`, `TargetConfig.SigningPolicy` |
| `src/config/forges.go`, `resolve.go`, `validate.go` | finder, resolver, validation |
| `src/sign/` (new pkg) | pure IR + compiler: `SignPlan` (requirements), `Compile`, `Enabled`, trust types (no cosign) |
| `src/sign/cosign/` (new pkg) | cosign **renderer** — `Render(plan,op,env)` (constraint solver), declared `Env`, `resolveKMSURI`, `signEnv`, executor fns (ALL cosign knowledge confined here; no interface/registry) |
| `src/build/docker/sign.go` | **delete** `CosignSign`/`CosignAttest`/`CosignAvailable` (logic moved to `src/sign/cosign`) |
| `src/build/docker/image_engine.go:184-193` | attach resolved policy to `RegistryTarget` |
| `src/build/docker/execute.go:455-462`, `record_outcomes.go:119-171` | `sign.Compile` → `cosign.SignImage/Attest` |
| `src/build/plan.go:76-85` | `RegistryTarget.SigningPolicy` |
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
      ↓  (compose many policies → many plans)
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

## Design framing & watch-points

This is a **constrained trust algebra**, not a signing feature: Policy = theorem, `SignPlan` =
normalized proof obligations, `Render` = witness construction, `Env` = the model of available
witnesses. It decomposes into three layers — (1) **identity semantics** (`policy → Compile → SignPlan`),
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
  reserved), and `Env` never becoming a *policy* channel. The next-level test is **adversarial `Env`
  construction**; for crypto classes derived-fingerprint grouping defeats it, for OIDC/capabilities the
  defense is attestation/claim-verification, which v1 explicitly does **not** yet do — so v1 must not
  overstate the trust of an un-attested `hardware` policy or an unverified `oidc` identity.

## Build/commit discipline
All build/test in the dogfood container (`golang:1.26.4-alpine3.23` for tests,
`prplanit/stagefreight:latest-dev` for `stagefreight commit`). Two commits
(`feat(signing): …` seam, then `feat(signing): sign SHA256SUMS …`). `chown` the tree
back to host uid after container commits before any host git op. Push to main; the dev
image push to dockerhub remains separately blocked on `docker login`.
