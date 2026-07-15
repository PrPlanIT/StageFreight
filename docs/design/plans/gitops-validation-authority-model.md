# StageFreight GitOps Validation — Authority Model & Schema Acquisition

> **Status: living design document.** The durable home for *how much StageFreight can trust a GitOps
> validation result, and how it acquires the authority to say so.* Companion to
> [gitops-fluxcd-validation.md](gitops-fluxcd-validation.md) (the per-Kustomization *verdict* model)
> — this document is about the **authority behind each finding** and the pipeline that raises it.

> **Phase A: SHIPPED** (operator-centric report + finding normalizer + trust-authority tiers).
> **Phase B: DESIGNED, not started** (dynamic schema acquisition from declared CRDs).
> **Reference-integrity axis: identified, not started.**

## 1. Motivation — the smell was real, the diagnosis was not "a bug"

The original GitOps Validation output was **tool-centric**: it printed kubeconform's transcript
verbatim — a raw jsonschema pointer, a catalog URL, the misleading `Check JSON formatting:` wrapper
(which implies a malformed manifest when the real condition is a *schema mismatch*), and one flat
`coverage — no schema` row per kind, all at equal visual weight. A heuristic advisory read like a
deployment failure. That is cognitive inflation: the renderer understood only `tool emitted output →
print output`, when the operator needs `finding → interpreted meaning → operational consequence`.

Repeated operator reaction — *"this is smelly, are these fixable or is there a bug?"* — was correct
to distrust the output, but the smell pointed at two different things that must not be conflated:

- **Fake certainty** — printing raw validator text with a wrong hint, or synthesizing operator
  semantics we cannot prove (*"the operator injects this field"* — folklore).
- **Resignation** — treating `schema unavailable` as an untouchable law of nature.

Both are rejected. The current unvalidated surface is **the limit of kubeconform's default schema
sourcing, NOT the limit of what StageFreight can do.** Those are not the same statement.

## 2. Empirical gap analysis — it is not a StageFreight bug (proven)

kubeconform was run **exactly as StageFreight invokes it** (`-ignore-missing-schemas -verbose
-output json -schema-location default -`) against three resources:

| Resource                     | kubeconform result | Why                                            |
| ---------------------------- | ------------------ | ---------------------------------------------- |
| `Deployment` (core)          | **valid**          | core schema present in the default set         |
| `CustomResourceDefinition`   | **skipped**        | the default set ships no CRD meta-schema       |
| `ExternalSecret` (custom)    | **skipped**        | no built-in schema; it is a CRD-defined API    |

StageFreight faithfully reports what kubeconform does; it is not swallowing schemas it could use.
The unvalidated kinds fall into two classes:

1. **`CustomResourceDefinition` itself** — kubeconform's default has no CRD meta-schema. Upstream
   gap, low value to close (a CRD *definition* is trusted upstream vendor content; meta-validating
   it buys little).
2. **Custom resources** (`ExternalSecret`, `Certificate`, `HTTPRoute`, `CiliumNetworkPolicy`,
   `HelmRelease`, `SecretStore`, …) — genuinely have no built-in schema; the community CRD catalog
   (datreeio) is **version-fragile** and misses the exact API versions in use, so they resolve to
   *unavailable* rather than *checked*.

### The deeper cause (and why it is a repo-hygiene signal, not only a tool gap)

For the reference deployment (`dungeon`), the CRDs that would define those custom resources are
**installed by Helm / operators, not vendored in the Flux tree** — so their schemas are absent from
the `kustomize build` output. The API surface is introduced **implicitly at runtime**. That
externalizes schema authority and weakens hermetic validation. StageFreight labeling those kinds
`○ schema unavailable` is therefore the **correct signal**: it marks precisely where the repo's
GitOps declarations are incomplete, rather than papering over it.

## 3. The core insight — CRDs *are* the schema distribution mechanism

Schemas do not fail to exist. **Kubernetes ships every CRD with its own authoritative schema**
(`spec.versions[].schema.openAPIV3Schema`). The problem is not absence — it is that **StageFreight
has no authoritative schema *acquisition* pipeline.** The current flow is:

```
manifest → kubeconform → { built-in schemas, optional heuristic catalog }
```

The correct flow is:

```
manifest set
  → discover CRDs (from declared sources)
  → extract OpenAPI schemas → construct an ephemeral, authoritative local schema registry
  → validate against THAT (then core, then heuristic fallback)
```

This is not hardcoding and not vendor-exception baking. It is **deterministic schema synthesis from
declared APIs** — a different category entirely. No live cluster required, because the CRDs already
carry versions, structural schemas, and OpenAPI definitions.

## 4. The trust-authority ladder (the unifying model)

Every validation claim carries **explicit authority provenance**. This mirrors StageFreight's
toolchain trust ladder (pinned digest → checksum → TOFU → unknown; see the toolchain trust model) —
one mental model, reused across domains. The operator learns to ask a single question: *what
authority backs this claim?*

```
extracted from a DECLARED CRD   authoritative-local   ← the repo's own truth; tried FIRST for custom resources
kubernetes core schema          authoritative-upstream
community CRD catalog           heuristic             ← fallback only; "may be stricter than your operator"
(none)                          unavailable           ← only for genuinely-undeclared APIs
```

Placement matters: an extracted, declared-CRD schema is *more* authoritative than the community
catalog **and** is the repo's own declaration, so it is consulted before core and before the
catalog for custom resources.

## 5. Phase A — the operator-centric report (SHIPPED)

Replaced tool-centric passthrough with a report that renders **operational meaning by authority
tier**. Implemented in `src/gitops/normalize.go` (the finding normalizer) and
`src/cli/cmd/ci_runners.go` (the renderer). Rendered form:

```
── GitOps Validation ─────────────────────────── 28.1s ──
│ scope        12 kustomizations · 2032 resources · 12 roots
│ tools        kustomize 5.5.0 · kubeconform 0.6.7
│
│ ✓ authoritative        12/12 kustomizations valid · 0 errors
│
│ ⚠ heuristic            1 · community CRD catalog, may be stricter than your operator
│    Vault/vault              spec.vaultContainerSpec.name — required by schema, not set
│      └ datreeio CRD-catalog · vault.banzaicloud.com/vault_v1alpha1
│
│ ○ schema unavailable   7 kinds · 25 resources — no published schema
│    CRD·13  Certificate·3  ExternalSecret·3  HTTPRoute·3  CiliumNetworkPolicy·1  HelmRelease·1  SecretStore·1
│      └ structurally validated by kustomize build; operators validate on apply
├─────────────────────────────────────────────────────────────
│ result       PASS · 12/12 valid · 1 advisory · 25 schema-unavailable
└─────────────────────────────────────────────────────────────
```

Invariants enforced (and tested):

- **Success reads first.** `✓ authoritative` precedes advisories — the operator establishes "valid"
  before contextualizing "there are advisories," which removes unnecessary anxiety.
- **Interpret, never dump; degrade, never fabricate.** `parseSchemaViolation` derives `(field, rule,
  schema-url)` from the validator output — every rendered statement mechanically derivable from
  schema + manifest + result. On a low-confidence parse it falls back to the raw message rather than
  synthesizing meaning. `SchemaFinding.Parsed()` gates interpreted-vs-raw. The misleading `Check
  JSON formatting` wrapper never reaches the primary surface (a test fails if it leaks).
- **Heuristic is framed, not dismissed.** Community-catalog findings say *"may be stricter than your
  operator"* — never "false positive," which would teach operators to ignore warnings. It preserves
  trust in the system while lowering false alarm.
- **Coverage is transparency, not deficiency.** `○ schema unavailable` is compressed into one
  grouped line (`N kinds · M resources — no published schema`) plus the reassurance that the
  resources are still structurally validated by kustomize build and operator-validated on apply.
- **Escape hatch: demoted, not destroyed.** The raw validator transcript (message + schema URL +
  jsonschema pointer) is retained in the audition artifact always, and in GitLab in a **collapsed
  fold** (`emitValidatorDetail`, gated on `IsGitLabCI()` so local stays clean). Advanced debugging
  keeps its raw data; the default surface keeps its clarity.

## 6. Phase B — dynamic schema acquisition (DESIGNED)

Three CRD sources, in increasing machinery and decreasing hermeticity. Each reuses the same
extractor + converter + ephemeral registry; only the *source of CRDs* changes.

| Rung   | Source                                                     | Hermetic?                    |
| ------ | ---------------------------------------------------------- | ---------------------------- |
| **B1** | in-tree `CustomResourceDefinition` (already in the render) | fully — offline, deterministic |
| **B2** | Helm chart `crds/` (resolve → pull → render HelmReleases)  | yes *iff chart versions pinned* |
| **B3** | OCI artifacts (Flux `OCIRepository`, by digest)            | yes, by digest               |

### 6.1 The load-bearing hazard — faithful conversion

A CRD's `openAPIV3Schema` is *structural* schema, not plain JSON Schema. It uses `x-kubernetes-*`
extensions:

- `x-kubernetes-preserve-unknown-fields` → must become `additionalProperties: true` (else valid
  extra fields are flagged)
- `x-kubernetes-int-or-string` → the `oneOf: [integer, string]` shape
- `x-kubernetes-embedded-resource` → embedded object semantics

**A naive converter that ignores these produces a schema STRICTER than the real API — and then
"authoritative-local" emits false positives on valid manifests.** That is worse than `unavailable`,
because it wears an authoritative badge and erodes trust. The invariant therefore mirrors the
normalizer's: **extracted authority must be faithful, or it must not claim authority.** The
converter's transform tests are the deliverable, not an afterthought.

### 6.2 B1 is the foundation

B1 (in-tree CRDs → faithful converter → ephemeral registry → wired as the first `-schema-location`,
ahead of core and catalog) is where the correctness risk lives and what every later rung stands on.
For `dungeon` it converts `HelmRelease` and the snapshot kinds — modest coverage, but `HelmRelease`
is a high-frequency Flux failure surface, and the point of B1 is to *prove the extractor is
faithful* before bolting Helm/OCI rendering onto it.

## 7. Benefits and honest limitations

**Benefits.** Validating a custom resource against its own CRD schema catches, at audition /
pre-merge, a real class of defects that today escape to `flux` reconcile: missing **required**
fields, **wrong types**, **enum** violations, **unserved apiVersion**, and (for strict CRDs)
field-name typos.

**Limitations, stated plainly.**

- **Mostly shift-left, not otherwise-undetectable.** The API server rejects these at apply anyway;
  the win is *earlier* and *pre-merge* (fail the PR, not the cluster) — real, but not "catching the
  uncatchable."
- **Value is contingent on faithful conversion** (§6.1). Careless conversion is noise pretending to
  be authority.
- **Residual: operator-self-installed CRDs.** Some operators install their own CRDs at runtime, not
  shipped in any chart `crds/`. Those cannot be extracted from any render — only from a live cluster
  — and remain `unavailable`. This is not a StageFreight limit; it is the repo failing to fully
  declare its API surface. The honest label is the signal.

## 8. The complementary axis — reference / wiring integrity

Schema validation is the **per-resource well-formedness** axis; it validates each resource *in
isolation*. It structurally cannot catch **cross-resource wiring errors** — the failures that most
often bite in GitOps:

- an `ExternalSecret` pointing at a `SecretStore` that isn't declared
- an `HTTPRoute` referencing a `Gateway`/`Service` that doesn't exist
- a `HelmRelease` referencing a `HelmRepository` not in the repo

StageFreight already has the bones (the `dependsOn` cycle / dangling-reference graph checks in
`graphVerdicts`). The strongest correctness posture **pairs** authoritative schema validation (B1/B2)
*with* reference integrity. This axis is arguably the higher-value *second* move; it is recorded
here so the roadmap does not mistake "more schema coverage" for "complete correctness."

## 9. Non-goals / rejected approaches

- **Hardcoding vendor exceptions** (e.g., a Vault `vaultContainerSpec` special case). Fake certainty.
- **Baking operator semantics into the renderer** (*"the operator injects this field"*). Folklore,
  unprovable, rejected.
- **Accepting `unavailable` as permanent.** Resignation; contradicts §3.
- **Community catalog as the answer.** Heuristic only — version-fragile, and *stricter-than-reality*
  by nature. Fallback rung, never authoritative.
- **Live-cluster schema introspection as the primary source.** Useful, but non-hermetic; violates
  the audition's offline determinism. Reserved as a last resort, never the default.

## 10. Roadmap & decision points

1. **B1 — in-tree CRD extraction** (faithful converter + transform tests first). Foundation; fully
   hermetic; proves faithfulness.
2. **Then let data decide the second move:**
   - **B2** (Helm-rendered CRDs) — the big schema-coverage rung, deterministic only if HelmReleases
     pin chart versions; or
   - **Reference-integrity pass** (§8) — the wiring-error axis.
   Choose by which demonstrably catches more real defects in the reference repo.
3. **B3 — OCI artifacts.** Additive once B1/B2 exist.

Residual `unavailable` after all rungs is a **repo-declaration gap**, surfaced honestly — a prompt
to vendor CRDs in-tree (better GitOps CRD lifecycle), not a StageFreight defect.
