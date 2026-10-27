# StageFreight CI — the audition contract and how phases gate

This is the authoritative description of how the pipeline gates. The control lives in the
**ledger**, in StageFreight's own code — forge-agnostic, so `stagefreight run` reproduces it
locally with no forge. The forge **renders** status and **transports** the ledger; it never
decides.

---

## The ledger and its three consumers

Phases run as separate jobs (fresh checkout each). Each subsystem's outcome travels as its own
fragment, `.stagefreight/subsystems/<name>.json`; `ReadState` reads the local
`.stagefreight/pipeline.json` (`cistate.State`, `src/cistate/state.go`) and overlays every
fragment by name, so artifact downloads union instead of clobbering. Downstream phases READ that
ledger. Review scans a carried artifact only when it was built for the pipeline's commit
(`SF_CI_SHA`); otherwise it fails closed. Three consumers, three needs:

- **StageFreight (control)** — reads the ledger to decide proceed/skip/deny. The *only*
  consumer that influences behaviour.
- **Forge (render + transport)** — projects the ledger into a status (exit code / job colour)
  and delivers the ledger between jobs (artifacts). Presentation and delivery, never control.
- **Humans (narrate)** — explains the ledger.

---

## The audition contract

Audition publishes exactly one record — its contract about its subject (the triggering
source C):

| field | meaning |
|---|---|
| `Outcome` | what happened: `success` / `failed` |
| `Blocking` | **control**: may this subject become a distributable artifact? |
| `Replacement` | **lineage**: the commit (C′) that supersedes this subject, if a fix was produced |
| `Reason` | human text |

Two invariants (both unit-tested in `deriveAuditionContract` / `performGate`):

- **Blocking is the only control truth.** It is false ONLY when nothing blocks (runner
  healthy, no fatal finding, no remediable finding, tests pass, deps didn't error). A
  **remediated** source stays `Blocking` — the fix is in C′, not in this subject, so building
  it would ship unfixed source.
- **Replacement is lineage, never control.** Perform reads `Blocking` alone. `Replacement` is
  consumed by narrate / publish / the forge renderer — never by the build decision.

---

## Phase responsibilities

> Perform creates outputs. Review evaluates outputs. Publish is the only phase permitted to
> mutate external distribution targets — registry push, signing, forge releases, package and
> chart publication, remote retention.

This contract is backend-universal, not Docker-shaped:

- **Never assume a backend can rebuild.** Helm charts, packages, rendered docs, and release
  notes have no cache-rebuild semantics. "Publish rebuilds from cache" is a Docker
  optimization, never the framework model; publish distributes the bytes perform produced.
- **Cross-cutting contracts stay backend-neutral.** Credential references, cache keys, and
  rebuild instructions never go into the outputs/results manifests.
- **Publication requirements are intent.** Signing, attestation, and SBOM are declared in
  `outputs.json` (`sign`/`attest`/`sbom`), satisfied at publish, and recorded as evidence in
  `published.json`. Never result-only: that allows an artifact approved-for-signing to ship
  unsigned.
- **Review defaults to fail-closed.** A soft-fail review is advisory, not a gate, and is
  explicit project opt-in.
- **Retention follows ownership.** Deleting remote tags, releases, or package versions mutates
  a distribution target, so it runs in publish after the push, when the final remote set is
  known. Only local Docker daemon cleanup (`provider: local`) stays in perform — it is
  workspace hygiene, and the daemon does not survive into the publish job.

---

## Phase gating (in-code)

Each phase records its own contract; each downstream phase consumes the prior one — never
infers from file existence or exit codes:

- **Audition** (`depsRunner`) → records the `audition` contract on every exit (a `defer`,
  fail-closed: the zero value blocks). Inspect → Classify (Fatal voids / Remediable runs the
  deps update) → tests → deps update → commit (records `Replacement` = C′).
- **Perform** (`performPhaseRunner`) → `performGate` reads `audition.Blocking`. Blocking → do
  not build (fail-closed on a missing contract). Not blocking → build, record `build`.
- **Review** → gates on `build`; records `security`.
- **Publish** → `authorizePhase` denies on `build`/`security`; `IsBranchHeadFresh` skips a
  stale distribution.
- **Narrate** → reads the ledger; `narrateAuditionLineage` explains "remediated → C′" or
  "blocked — <reason>".

---

## Forge render + transport (the adapter — not control)

The forge does two jobs, both adapter concerns:

1. **Transport.** The ledger must reach the next job. Audition's artifacts are rendered
   `when: always` (`ArtifactSpec.WhenAlways`, GitLab `artifacts: when: always` / Actions
   `if: always()`), so the contract is delivered **even when audition failed** — otherwise a
   failed audition would drop its ledger and the in-code gate would be silently bypassed by
   forge behaviour. Audition is `allow_failure` so perform *runs* (and reads the contract)
   rather than the stage halting.
2. **Render.** The pipeline's Success/Warn/Fail is a **projection** of the contract via exit
   codes: perform (not `allow_failure`) hard-fails on a dead-end block; a superseded block
   skips green and shows Warn via the surrounding orange. Control never depends on it.

---

## The deps-autoremediation flow (the driving example)

The dependency-remediation bug is what exposed that Audition was the one phase publishing no
contract. The **chicken-egg**: a Critical (CVSS ≥ 7) `osv`/`freshness` finding made lint
abort before the deps update that would clear it ever ran. The fix: Classify lets a Remediable
finding through to the deps update, which remediates and commits C′; the contract records
`Blocking:true, Replacement:C′`; the follow-up pipeline on C′ lints clean and ships.

### State matrix (all in-code, via the contract)

| case | contract | perform | pipeline |
|---|---|---|---|
| clean | `Blocking:false` | builds | **Success** |
| bad lint (fatal) / unhealthy / tests-fail / deps-fail | `Blocking:true`, no repl | fail | **Fail** |
| Critical CVE, unremediable | `Blocking:true`, no repl | fail | **Fail** |
| Critical CVE, remediated | `Blocking:true`, C′ | skip (superseded) | **Warn** → C′ ships |

Nothing that carries an unresolved problem passes; a fixable CVE self-heals to a clean ship on
the follow-up pipeline.

---

## What is NOT part of control

- `security.fail_on_critical` (default false) — the image scan reports; it does not gate. The
  gate is the audition contract over the source. (An operator may still set it for defence in
  depth on the built image.)
- `handoff` (default `continue`) — how the *superseded* pipeline behaves after a fix commits;
  a rendering/efficiency choice, not the gate.
