# Audition as Transformation — the candidate lifecycle

> **SUPERSEDED (aspirational).** The shipped realization is the **audition contract** —
> see [`pipeline-flow.md`](pipeline-flow.md). That doc is authoritative for how the pipeline
> actually gates. This file records the broader "artifact stewardship" vision the contract
> work grew out of; its enduring insight — *every phase publishes one authoritative contract;
> downstream consumes, never infers* — is what shipped. The grander framing here (two-pass
> materialization, commit-in-Publish) did **not** ship and should not be treated as current.
> Kept for the reasoning, not as a spec.

Status (historical): design model. This records the target shape of the Audition phase and
the phase contracts it implies. Read `pipeline-flow.md` for what was actually built.

---

## The reframe

Audition today is procedural — "a list of things we run" (lint, tests, deps). The
target model is a transformation:

> **Audition transforms a source tree into the best valid candidate StageFreight
> is authorized to produce. If no valid candidate can be produced, the phase
> fails without side effects.**

Everything downstream then operates on an explicit, progressively-refined
artifact rather than an implicit workspace. That is the same move already made
with immutable outputs, intent-vs-results manifests, and digest identity —
applied to the input side of the pipeline.

---

## The core law

> **Nothing IRREVERSIBLE becomes durable until every upstream authority has
> accepted it.**

The load-bearing word is *irreversible*, and the distinction is reversibility:

- A **git commit to a branch is reversible** — a revert or fix-forward restores
  state. So it may be gated by a *proportionate* authority: Audition's lint + tests.
- **Distribution is irreversible** — an image push or a release may already have
  been pulled by consumers. So it is gated by *every* authority: Perform built it,
  Review evaluated it, `authorizePhase` cleared it.

StageFreight already draws exactly this line — its phase authorization "gates only
irreversible/externalizing actions." Every other rule here is a corollary. When
evaluating a change: *does it let something irreversible happen before every
authority accepted it? then it is wrong.* A reversible source commit riding a
lighter gate is not a violation — it is the law applied in proportion.

---

## Phase contracts (authorities)

The five verbs are unchanged — they are project identity. What sharpens is the
authority each holds.

| Phase        | Authority                                                    |
| ------------ | ------------------------------------------------------------ |
| **Audition** | Steward the candidate.                                       |
| **Perform**  | Produce outputs from the candidate.                          |
| **Review**   | Decide whether outputs are acceptable to distribute.         |
| **Publish**  | Materialize approved state into authoritative systems.       |
| **Narrate**  | Explain the resulting state to humans and other systems.     |

A candidate is not "valid" after Audition alone. Validity accretes, and durability
is granted in proportion to reversibility:

```
Source
  → Audition   (local validity — lint + tests; enough to COMMIT, a reversible act)
  → Perform    (build validity — it actually builds)
  → Review     (distribution validity — it passes image policy)
  → Publish    (DISTRIBUTION — the irreversible act, gated by all of the above)
```

Note the commit lands at Audition, not Publish — see "Why the commit lives in
Audition" below. The architecture makes that mandatory, not a choice.

---

## Audition internals — five verbs, deps as the first mutator

```
Inspect → Classify → Mutate → Validate → (verdict)
```

- **Inspect** — pure observation. Produce findings (module, severity, remediable).
  No verdict, no CI semantics. This is the lint engine, but its result is
  *findings*, not a bare pass/fail. (`RunLint` must surface findings, not just
  `(string, error)`.)
- **Classify** — the only classification that matters: **Fatal vs Remediable**,
  answering *"is it safe for StageFreight to mutate this repository?"* — a
  mutation-safety question, **not** a CI question. Reuse the existing
  `worldModules = {freshness, osv}` (`src/lint/baseline_diff.go`): a blocking
  finding from a world module is Remediable; anything else (secrets, conflicts,
  broken tree, failing gate tests) is Fatal and voids the source.
- **Mutate** — apply remediators. **Deps is the only mutator today** and stays
  concrete. Do **not** build a `Mutator` interface until a second mutator (gofmt,
  generators, SBOM) reveals what is genuinely common — extract at N=2, never
  speculatively. Mutators communicate only through repository state; a normalizer
  (formatting) runs last.
- **Validate** — inspect the *candidate*, not the source. This is the verdict, and
  it is the single source of authorization truth. No carried state: a blocking
  finding on the input tree that the mutation cleared is **not** a failure —
  StageFreight authorizes the candidate it produced, not the input it received.

### Why the verdict must come from Validate alone

The security-critical property is structural, not vigilant: if the verdict is the
final `Validate(candidate)` and nothing else, there is **no exit path that can
authorize while a blocking finding survives**, because the only verdict that
exists is computed last, over the candidate. The current `depsRunner` violates
this — `runUniversalLint` returning an error aborts *before* the deps update that
would remediate (the chicken-egg: the gate blocks the fix that clears the gate),
and several `return nil` paths past the lint gate (re-verify failure, run_from
mismatch) would mark the audition PASS while a blocking finding is unremediated.
The fix is one verdict, derived from Validate, at one exit.

---

## Autoremediation — two passes via commit + restart, no MR

Autoremediation is deliberately implicit and automatic; it is an *accelerator to
correctness*, not a pull-request chore. It runs inline. The "two passes" are two
*pipelines* joined by a commit — because of how StageFreight's phases carry state.

### Why the commit lives in Audition (the architecture forbids anything else)

Each phase is a separate CI job with a **fresh git checkout**; only `.stagefreight/`
crosses job boundaries as an artifact (`src/ci/render/planner.go`). A mutated
`go.mod` lives at the repo root, *outside* `.stagefreight/`, so **a mutation made in
Audition never reaches Perform, Review, or Publish** — they re-clone clean HEAD.
`deps.patch` is a StageFreight-native inspection artifact, not git-apply-compatible,
and consumed by nothing. So the *only* way a mutation reaches later phases is to be
committed and re-cloned. The commit therefore MUST happen in Audition — the sole
phase with a mutable workspace — and Publish literally cannot commit a mutation it
never sees.

This hands us an invariant for free: because every phase re-checks-out a committed
SHA, **Perform always builds an immutable committed candidate, never a workspace.**
The model does not engineer this; the phase architecture guarantees it.

### Pass 1 — remediation (this pipeline, on HEAD)

```
Audition  Inspect → Classify → Mutate(deps) → Validate(candidate: lint + tests) ✓
          → commit C′ (advances branch HEAD)
          → handoff: this pipeline is now stale → cancel it
```

The commit is gated by Audition's proving — lint + tests on the mutated tree — which
is *proportionate*: a commit is reversible. It is NOT gated by Perform/Review, and a
build-free Audition cannot make it so (see the residual). The commit advances HEAD,
which makes this pipeline stale (`SHA ≠ remote HEAD`); `EvaluateHandoff`
(`src/ci/handoff.go`) cancels it, and if it runs through anyway, `IsBranchHeadFresh`
makes Publish skip distribution ("a newer pipeline will ship",
`src/cli/cmd/ci_phases.go:257`). Either way the stale tree is never distributed —
*that* is "gate distribution on the mutation," realized as staleness.

### Pass 2 — production (a fresh pipeline, on C′)

```
Audition  Inspect → clean → no mutation → Validate ✓
Perform   build the checked-out committed C′   ← the artifact
Review    evaluate outputs (image policy) ✓
Publish   distribute: registry, release, signatures, provenance (keyed to C′)
```

The remediation commit (skip_ci false) naturally triggers this fresh pipeline. It
re-runs the full sieve against C′ — build and image policy included — and only here,
after every authority has accepted, does the irreversible distribution happen. This
is also why the second pass is not a waste: trust is re-earned, never inherited
(below).

### The residual, stated honestly

Because Audition does not build — it is the light "proving" phase
(`DockerRequired: false`) and stays that way — the *commit* is gated on lint + tests
only, not on Perform (build-specifics) or Review (image policy). So a bump can pass
Audition and fail Pass 2. When it does: distribution is blocked (nothing ships), and
you are left with a transient, reversible, loudly-red commit on the branch. That is
the accepted price of a build-free Audition — proportionate, because the commit is
reversible and the irreversible act stays fully gated. `go test ./...` (which
compiles every package) plus the candidate re-lint make this residual small: a bump
that won't compile or breaks a tested path dies before the commit.

---

## Trust is re-earned, not inherited

Every pipeline runs the full sieve against its *own* checked-out commit, with **no
fast path that trusts a previous run's verdict.** The C′ pipeline is where build and
image policy are checked for the first time — Pass 1 only proved lint + tests before
committing — so it is not redundant re-validation; it is the *first* full
validation, and it happens against current world-state. A
skip-because-a-prior-pipeline-passed button is a known gap: it inherits *stale*
trust. A commit clean yesterday but named by a CVE published overnight is caught on
its next pipeline precisely because `freshness`/`osv` are re-run against current
world-state, never remembered as a verdict. Nothing irreversible ships on inherited
trust — every distribution passed the full sieve against reality as it stood at that
run. The second pass is this principle in action, not a cost to optimize away;
optimizing it would buy speed by manufacturing the gap the model exists to close.

A corollary at the release boundary: a tag pipeline that finds a blocking finding
with mutation disabled (tags do not mutate — Slice 2) correctly **fails** rather
than shipping. You cannot silently publish mutated source under a tag that names
the pre-mutation commit; the remediation lands on the branch first, then a clean
commit is tagged. An operator who explicitly ungates lint or CVE findings owns that
risk — the tool does not relax on its own.

---

## Materialization — two authority classes, gated by reversibility

There are two kinds of durable write, gated in proportion to how reversible they are
and — decisively — by which phase owns the workspace:

- **Source-of-truth mutation** — git commits to the repo: Audition's remediation
  commit (C′), Narrate's docs/badges. **Reversible.** It happens in the phase that
  *owns the workspace* — Audition commits the deps mutation, Narrate commits docs —
  gated by that phase's own proving (Audition: lint + tests). Publish cannot do it:
  it re-clones clean HEAD and has no mutation to commit.
- **Distribution** — externalizing artifacts to consumers: registries, releases,
  signatures, provenance. **Irreversible.** **Publish only**, and only after
  Perform + Review + `authorizePhase` clear it, and only when the pipeline is fresh
  (`IsBranchHeadFresh`).

This sharpens the older "Publish is the sole external mutator" invariant, which was
imprecise. The true invariant: **Publish is the sole *distribution* materializer;
source commits happen where StageFreight owns the workspace (Audition, Narrate).**
Both are "authority precedes capability" — SF has authority over its source and over
its distribution — but they are gated in proportion to reversibility, and located by
which phase can actually see the mutation.

---

## Outcomes — tri-state

CI's binary success/fail is too coarse to describe stewardship. StageFreight's
outcome is tri-state (native to SF):

- **PASS** — nothing to remediate; the trigger commit is the candidate; build it.
- **REMEDIATED** — Audition produced a valid candidate and committed it as C′;
  `EvaluateHandoff` then cancels this (now-stale) pipeline and the fresh C′ pipeline
  builds + ships it. Not a failure — a successful stewardship. On the forge this
  surfaces as a cancelled pipeline plus a fresh one on C′; surfacing "REMEDIATED"
  explicitly in SF's own output is an operator-clarity nicety over a mechanism that
  already does the right thing.
- **FAIL** — no valid candidate could be produced.

The REMEDIATED chain is **bounded by construction**: REMEDIATED is emitted only
when Validate confirms the candidate clean; an oscillating or unfixable mutation
fails Validate *within* the one pass → FAIL, never a loop. The chain extends only
for genuinely-new findings (e.g. a CVE published between runs), which is correct.

### The push race converges via staleness

If the branch HEAD moves between Audition reading it and pushing C′, the push is
refused (non-fast-forward) — `main` *declined the candidate*. Nothing durable
happened, no side effects. Do not rebase in place. And it self-heals: whoever
advanced HEAD made *this* pipeline stale, so `IsBranchHeadFresh` skips its
distribution anyway, and the pipeline that won the race (or the next one) re-runs
remediation from the new head. Staleness is the convergence mechanism — a lost race
just means another pass tries, and the loser never distributes.

---

## What this replaces, and how it ships

The transitive-CVE remediator is already built and proven (batched pin +
conflict-resolution + verify-reality; dogfooded green on hasteward:
`x/net`→0.55.0, `x/sys`→0.45.0, 9 CVEs fixed). The gap was purely that the lint
gate aborted before it ran. Crucially, **the two-pass is mostly already in the
codebase** — commit-in-audition + `EvaluateHandoff` restart + `IsBranchHeadFresh`
distribution-skip are the two passes. So this is a small correctness fix over
existing machinery, not a new phase pipeline. Sliced so the security gate is never
refactored blind:

- **Slice 0 — surface findings (behavior-preserving). DONE.** `[]Finding` threaded
  up `runPreBuildLintImpl → RunLint → runUniversalLint`; `depsRunner` aborted
  exactly as before. Build-verified.
- **Slice 1 — classify + verdict-from-candidate. DONE.** `lint.Classify` (pure,
  unit-tested, reuses `worldModules`); `depsRunner` now Inspect → Classify →
  [fatal ⇒ abort] → Mutate → re-Validate the candidate. A remediable finding no
  longer aborts (the chicken-egg); a remediation that fails to clear the finding, or
  breaks tests while remediating, FAILS rather than silently passing.
- **Remaining (small):** confirm the deps-commit config triggers the restart
  (skip_ci false + handoff `restart_pipeline`) so Pass 2 fires on its own; optionally
  surface `REMEDIATED` in output; **Slice 2** — mutation-mode (tags do not mutate;
  a tag pipeline with a blocking finding FAILS rather than shipping).

Deps stays the only mutator. The `Mutator` interface and other modalities (GitOps,
Terraform, OCI, docs) are **not** built here — they extract when a second real case
constrains their shape. Principles constrain decisions; they do not imply scope.
