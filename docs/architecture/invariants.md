# StageFreight — Hard Invariants

These rules are non-negotiable. They exist because we proved them structurally, not because someone wrote them down. Every item here was enforced in code before it was written here.

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

## Adding a new invariant

Before adding a new invariant here:
1. Enforce it in code first (comment, test, or panic guard)
2. Verify the enforcement passes
3. Then document it here

An invariant that lives only in this file is not an invariant — it is a wish.

---

## Related: not-yet-enforced design models

Some constraints are known but cannot yet be enforced because the code that would
violate them does not exist. These live as design models, not invariants, until
there is something to enforce against:

- [`persistence-identity.md`](persistence-identity.md) — the persistence-handle
  algebra and the cross-phase transformation constraints a **second content
  store** must satisfy. Records a discovered coupling (the capability axis and the
  handle-representation axis must evolve together) that is real but unenforceable
  until a second store exists. Read before implementing one.

- **Mutation safety — the second of two distribution concerns.** A distribution
  capability answers two independent questions, with one home for each: (1) *should
  this fire?* — eligibility, Invariant 7, mechanically enforced; (2) *is it safe to
  mutate?* — mutation safety, recorded here because it is not statically detectable.
  The PROPERTY: a mutation of **mutable shared state** — a rolling registry tag
  like `latest-dev`, a package channel, a repository write-back (docs/badges),
  retention/prune — must be freshness-safe, whereas **immutable** publications (a
  digest, a version-pinned tag, a `v1.2.3` release) are inherently freshness-
  independent. The abstraction is mutable shared pointer/state vs. immutable
  artifact, not any release/package concept. This is stated as a property on purpose, deliberately NOT bound to a
  mechanism: `ci.IsBranchHeadFresh` is the *current* implementation (a conservative,
  whole-operation, event-level approximation that gates the whole op on a branch
  pipeline and exempts tags), and a future per-target `MutationPolicyAllows` may make
  it precise — letting freshness-safe immutable publications through while still
  blocking rolling moves — without changing the property. It is not a hard invariant
  because "mutates rolling state" cannot be detected from the AST; it lives here
  until there is something to enforce against. Binding the rule to today's
  `IsBranchHeadFresh` mechanism would recreate the documentation trap that the
  freshness work itself removed.
