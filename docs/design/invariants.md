# StageFreight — Hard Invariants

These rules are non-negotiable. They exist because they were proven structurally, not because someone wrote them down. Every item here was enforced in code before it was written here.

If you are about to violate one of these, stop. Discuss it first.

---

## 1. Config truth has exactly one construction path

> No executable StageFreight path may obtain runtime config except through `loadResolved`.

**What this means:**
- All runtime config flows through `src/config/config.go:loadResolved`
- `LoadWithWarnings` and `LoadWithReport` are thin wrappers — they call `loadResolved`, nothing else
- Raw `yaml.Unmarshal` / `yaml.NewDecoder` into a `Config` struct is forbidden outside `src/config/`

**Why:**
- `loadResolved` is the only place where presets are resolved before struct decode
- Bypassing it produces a `Config` that has not had presets applied — execution diverges from what the operator declared
- Split-brain config (report says one thing, execution does another) is the failure mode this prevents

**Enforcement:**
- `src/config/invariants_test.go` — fails CI if any file imports `config` and uses raw YAML decode
- `loadResolved` carries a comment that names it as the only entry point

---

## 2. Preset resolution happens before struct decode, always

> Presets are applied to the raw YAML map. The resolved map is then decoded into `Config`. These steps are never separated.

**What this means:**
- `preset.ResolvePresets(rawMap, ...)` runs on the raw `map[string]any`
- The resolved map is re-marshalled and decoded via `yaml.NewDecoder` with `KnownFields(true)`
- Validation and normalization run after decode, on the resolved struct

**Why:**
- Decoding first and resolving after means field defaults interact unpredictably with preset values
- `KnownFields(true)` ensures unknown keys from malformed presets fail loud, not silently

---

## 3. SectionState active/provenance contract

> `Active == false` → `Provenance` MUST be `"none"`
> `Active == true`  → `Provenance` MUST NOT be `"none"`

**What this means:**
- Inactive sections have no provenance — they do not exist in the runtime model
- Active sections must declare where they came from: `"manifest"` or `"preset"`
- The string `"none"` is not a valid provenance for an active section

**Why:**
- The Config panel renders provenance for active sections. A `"none"` provenance on an active section is a lie of omission.
- An inactive section with a non-none provenance means the rendering logic is inconsistent with the execution model

**Enforcement:**
- `SectionState.validate()` is called on every `SectionState` construction and panics on violation
- This is a programmer error, not a runtime condition — panic is correct

---

## 4. Output system layer contract

> Rendering writes. Layout decides shape. Terminal decides constraints. These three layers never merge.

**What this means:**
- `src/output/layout/` — pure formatting math: ANSI-transparent width, word-boundary wrapping, value column detection. No I/O.
- `src/output/termutil/` — terminal constraints only: converts a writer to a content width budget. No formatting.
- `src/output/section.go` — rendering only: calls layout with termutil budget. No wrapping logic.

**Why:**
- Merging layout into rendering means layout cannot be tested without I/O
- Merging terminal detection into layout means layout logic is untestable in CI

**Enforcement:**
- `src/output/layout/wrap_test.go` — 18 tests covering ANSI transparency, emoji width, word-boundary wrap, hard-cut ellipsis, continuation indent

---

## 5. Panel domain ownership — one datum, one panel

> No datum appears in more than one panel. No datum appears before its domain panel.

**What this means:**
- `DomainCode` (Code panel): Commit SHA, Branch/Tag only
- `DomainExecution` (Runner panel): Engine, Pipeline, Job, substrate facts
- `DomainConfig` (Config panel): source file, presets, resolution state
- See `src/output/domains.go` for the full registry

**Why:**
- Duplication creates observable inconsistency when one copy updates and the other doesn't
- Early leakage (e.g., Registries in ContextBlock) means the Code panel is no longer a stable identity panel

**Enforcement:**
- `DomainKV` type + `ContextBlock(w, []DomainKV, color)` — ContextBlock only accepts typed KVs; passing a non-Code domain is structurally visible in review
- `src/output/domains.go` is the authoritative domain registry

---

## 6. `...` ellipsis is for hard mid-token cuts only

> Word-boundary wraps are clean. Ellipsis appears only when a single unbreakable token is hard-cut.

**What this means:**
- A row that wraps at a word boundary produces clean continuation lines indented to the value column — no decoration
- A row that cannot find a word boundary within budget is hard-cut with `...` suffix on the cut piece

**Why:**
- `...` on every wrapped line is screen clutter that degrades readability
- The operator should see value tokens, not wrap artifacts

---

## 7. Target eligibility has exactly one interpreter

> Outside `src/config`, code may ask whether a target matches (`TargetMatches` / `TargetMatchesEnv`) or whether it is unconditional (`TargetIsUnconditional`), but may NOT read `When.Events`, `When.Branches`, or `When.GitTags` directly.

**What this means:**
- `when:` routing — events, branches, git_tags, named patterns, `re:` inline regex, `!` negation — is interpreted in exactly one place: `src/config` (`TargetMatches` / `TargetMatchesEnv`, `ResolvePatterns`, `TargetIsUnconditional`)
- Every capability (docker, binary archives, release, retention, sync, package, and each future one) consumes that API; none inspects the `When` fields itself
- A new eligibility dimension is added to the matcher, never bolted onto a caller

**Why:**
- Per-capability interpreters drift. Docker's former `targetAllowed` gated branch and tag but not the event, so a manual pipeline distributed an image while every event-gated capability correctly skipped. Each new capability is another chance to reintroduce that divergence.
- One interpreter means `events`/`branches`/`git_tags`/named-pattern/negation mean the same thing everywhere, and a routing fix lands once

**Enforcement:**
- `src/config/eligibility_routing_test.go` (`TestNoDirectWhenAccessOutsideConfig`) walks the source tree and fails CI on any non-test `When.{Events,Branches,GitTags}` access outside `src/config`

---

## 8. Reconciliation derives; it never deliberates

> A reconciler makes the repository consistent with intent it already encodes. It may
> only mutate state functionally dependent on an authoritative source, and it never
> selects among valid futures.

**What this means:**
- Two mutation classes are distinct: *intentful updates* (choose among valid futures — governed by `max_update`, holds, review) and *reconciliation* (derive the single representation the repo already requires — no policy, no discretion)
- Authority is one-directional: `go.mod`'s `go` directive is authoritative for the golang builder image; builder → `go.mod` is not a reconciler (that is soft normalization of valid config)
- Reconciliation is not bounded by `max_update` — nothing is being decided
- The target is a *canonical satisfying representation*: the floor's own minor line, newest stable patch, operator variant and tag granularity preserved
- Derivable-or-fail: when no canonical representation exists (variant absent on the floor's minor), emit a configuration error — never drift variant, overshoot the minor, or change granularity

**Why:**
- `go mod tidy` can raise `go.mod`'s floor in the same run the builder is pinned below it; treating the result as an "update" (bounded by `max_update`, held for review) ships a repo that cannot build
- A repository inconsistency is not an upgrade decision — modeling it as one invents intent the repo did not encode

**Enforcement:**
- `src/reconcile/` is a policy-free package (no `max_update`, no candidate/hold types); the go-toolchain reconciler is a pure function over observations
- `src/reconcile/gotoolchain_test.go` asserts minimal-satisfying + variant/granularity preservation + fail-closed on unrepresentable variants; `src/dependency/reconcile_test.go` asserts the derived edit lands and unsatisfiable floors surface config errors

---

## Adding a new invariant

Before adding a new invariant here:
1. Enforce it in code first (comment, test, or panic guard)
2. Verify the enforcement passes
3. Then document it here

An invariant that lives only in this file is not an invariant — it is a wish.

---

## Related: design models (repo-only)

Some constraints are known but can't be enforced yet, because the code that would violate
them doesn't exist — a second content store, a per-target mutation-safety policy, the
publish→transport boundary. Those are recorded as design models in the repository's planning
notes (`architecture/plans/not-yet-enforced-design-models.md`); each becomes a numbered
invariant here once there's something to enforce against.
