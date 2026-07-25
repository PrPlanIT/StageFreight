# Lifecycle mode — validation (A) + mode-table consolidation (B)

> ## STATUS — SHIPPED (A + B, corrected)
> The final implementation differs from the original (A)/(B) split below in two ways the
> review forced:
> - **Validation = mode ALLOWLIST only.** The per-mode "legal-block matrix" was **removed as
>   overreach**: the phases run for every mode and an off-mode block is *inert* (perform
>   reconciles, publish is not-applicable), so gating `builds:`/`publish:`/`governance:` would
>   bake in single-mode exclusivity and reject legitimate/future multi-mode configs. The only
>   check is: unknown `lifecycle.mode` → error (closes the typo→silent-build footgun).
> - **One source of truth: `src/config/mode.go`.** `ModeImage/Gitops/Docker/Governance` consts
>   + the `lifecycleModes` table (`PhaseReconcile`, `Backend`) + `LookupMode`/`Config.Mode()`.
>   The validator and **every** dispatch site read it — the 4 `ci_phases` switches, `RunLifecycle`,
>   `capability.go`, `reconcile.go`, `docker.go`, `governance_reconcile.go`, `trust.go` — replacing
>   the scattered `case "gitops","governance"` literals. Behavior-preserving: `ci render gitlab`
>   is byte-identical; SF loads; full suite + a table-equivalence test green.
>
> The analysis below is the original pre-implementation design, kept for the reasoning. Read
> the STATUS box for what actually landed.
>
> Pre-implementation design. **(A)** is a safe, additive, `validate.go`-only change that
> closes the silent-typo footgun and adds per-mode block legality. **(B)** is a
> behavior-preserving refactor that collapses the scattered mode-dispatch into one table.
> Ship (A) first; (B) depends on (A) having run (see B3).

## Context — how mode is decided today

`lifecycle.mode` is a single scalar (`config.LifecycleConfig.Mode`, doc: *"empty defaults to
image"*). There is **no central mode logic** — the mode→behavior mapping is a copy-pasted
2-case switch scattered across the codebase:

```go
mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
switch mode {
case "gitops", "governance":   // → reconcile
default:                       // "image" or "" → build   ← the image default lives HERE
}
```

The switch appears **inside the universal phase runners** (`src/cli/cmd/ci_phases.go`
~126/162/222/239) — it selects the phase *body*, not the pipeline. It also appears in
`RunLifecycle`'s own switch (`src/runtime/phase.go:71-80`, `gitops→cfg.GitOps.Backend`,
`docker→cfg.Docker.Backend`), and governance's standalone `governance reconcile` command
(`Mode == "governance"` at `governance_reconcile.go:222` is only a CI-source-inference hint).
`validate.go` gates **nothing** about mode.

### The pipeline is universal; mode dispatches phase *behavior* (ground truth)

**Audition → perform → review → publish → narrate is the ONE job graph — every mode runs all
five phases.** Mode is **not** a pipeline selector and is **not** gated to any phase. What
varies per mode:

- **What a phase body does.** `performPhaseRunner` is universal; inside it, mode chooses:
  `image` (or empty) → **build**; `gitops`/`governance` → **reconcile** (`reconcileRunner` →
  `RunLifecycle` → backend). Same phase, different act. `RunLifecycle` is the reconcile engine
  invoked *from within perform* (and standalone via `stagefreight reconcile`) — **not** a
  separate pipeline that image "skips."
- **Which config blocks are legal** (the A2 matrix) — `builds`/`publish` (image) vs `gitops:`
  / `docker:` / `governance:`. Everything else is shared across all modes.

Governance is dispatched by its **own subsystem** (policy distribution), not a
`LifecycleBackend` — but it still runs inside the same universal phases.

### Asymmetries that constrain the design

1. **The image default is the `default:` case** of the in-phase switch — any unset *or unknown*
   mode → the build body. This is why SF (no `lifecycle:`) works — **and** why a typo
   (`gitpos`) silently runs the *build* body instead of erroring. (A) closes this.
2. **`docker` isn't in the in-phase switch** — perform's switch is `case "gitops","governance"`,
   so a `docker`-mode config hits `default:` → the *build* body in CI (docker reconcile is
   reached via the `docker`/`reconcile` CLI, whose `RunLifecycle` switch *does* know docker).
   The two switches **disagree on the mode set** — a latent gap, and the table in (B) must
   reproduce it exactly (don't "fix" it silently here).
3. **`governance` has two entry points** (CI perform's `reconcileRunner` + the standalone
   command) and is a distinct subsystem. It stays out of the `LifecycleBackend` interface.

### Invariants — must load/dispatch **unchanged** (the regression list)

- **SF**: no `lifecycle:` → perform runs the **build** body (the `default:` case).
- **dungeon**: `lifecycle.mode: gitops` + `gitops.backend: flux` → perform runs the
  **reconcile** body (`reconcileRunner`).
- **docker** CLI reconcile; **governance** both entry points.
- The five phases emit for **every** mode — validation must never gate a phase on mode.
- Do **not** flatten the scalar, do **not** make `lifecycle:` required.

---

## (A) Scoped design — mode validation + legal-block matrix

**Goal:** a single validation pass that (1) rejects unknown modes and (2) rejects
mode-specific blocks that don't belong to the resolved mode. **`validate.go` only. Zero
dispatch changes.**

**Non-goals:** no scalar flatten, no dispatch consolidation, no governance refactor, no
`LifecycleMode` type (that's (B)).

### A1 — Mode allowlist + canonicalization

Canonical set: `{image, gitops, docker, governance}`; **empty ≡ image** (matches the live
`default:` behavior — empty must stay legal). Normalize with the same
`strings.ToLower(strings.TrimSpace(...))` the dispatch uses, so validation and dispatch agree
byte-for-byte.

```
rule: canon(mode) ∈ {image, gitops, docker, governance}
  else → ERROR "lifecycle.mode: unknown mode %q (expected: image, gitops, docker, governance)"
```

This alone kills asymmetry #1's footgun.

> ### ⚠ REVIEW CORRECTION — the naïve legality check breaks every config
> `loadResolved` does `cfg := defaults()` **then** decodes YAML over it (`config.go:217`), and
> `defaults()` sets `GitOps.Backend: "flux"` **and** `Docker.Backend: "compose"` on *every*
> config (`config.go:286-288`). So any content sentinel on those fields
> (`cfg.GitOps.Backend != ""` / `cfg.Docker.Backend != ""`) is **always true** — a legality
> check on `gitops`/`docker` would error on SF, dungeon, and everything. **Do not gate
> `gitops`/`docker` by content.** Only blocks with **non-defaulted** sentinels are safe to
> check: `builds`, `publish`/`targets` (maps, zero-value), `governance` (`Clusters` slice,
> zero-value). The `gitops`/`docker` presence checks are *deferred* — they need presence
> detection (authored top-level keys from `rootNode`, not `Config` content) and are the
> lowest-value checks anyway (an inert block in the wrong mode does nothing).

### A2 — Legal-block matrix (safe subset)

Only mode-specific blocks with a **non-defaulted** sentinel are gated. Every shared block (`ci`,
`vars`, `git`, `forges`/`repos`/`registries`/`signing`, `commit`/`tagging`/`release`/`glossary`,
`scribe`, `lint`/`test`/`security`/`dependency`, `toolchains`, `manifest`, `build_cache`,
`defaults`, `preset_source`, `version`) is never gated.

| block | sentinel | defaulted? | legal only in | gate now? |
|---|---|---|---|---|
| `builds` | `len(cfg.Builds)>0` | no | image | ✅ |
| `publish` | `len(cfg.Targets)>0` | no | image | ✅ |
| `governance` | `len(cfg.Governance.Clusters)>0` | no | governance | ✅ |
| `gitops` | `cfg.GitOps.Backend` | **yes (flux)** | gitops | ⛔ deferred |
| `docker` | `cfg.Docker.Backend` | **yes (compose)** | docker | ⛔ deferred |

```
if len(cfg.Builds)  > 0 && mode != image      → ERROR "builds: valid only in lifecycle.mode: image (got %s)"
if len(cfg.Targets) > 0 && mode != image      → ERROR "publish: valid only in lifecycle.mode: image (got %s)"
if len(cfg.Governance.Clusters) > 0 && mode != governance
                                              → ERROR "governance: valid only in lifecycle.mode: governance (got %s)"
```

Direction that matters most is covered: a **gitops/docker/governance repo that authors
`builds`/`publish`** (a real, harmful mistake — it would build/push where it shouldn't) is
caught. The *reverse* (a stray `gitops:`/`docker:` in an image repo) is inert and deferred.

### A3 — Why content sentinels, and only these three

`builds`/`publish`/`governance` are **zero-value in `defaults()`** (verified), so `len()>0`
means *the YAML actually wrote them* — no presence plumbing needed, no false positives. This is
the whole reason the check is safe. `gitops`/`docker` are **not** zero-value in `defaults()`, so
`len`/`!= ""` can't distinguish authored from default — they require reading `rootNode`'s
top-level keys (deferred, see the correction box).

### A4 — Error, not warning

The three checks are **errors** — a gitops/governance/docker repo carrying `builds:`/`publish:`
is a genuine, harmful misconfiguration, and house style is fail-loud. Verified safe: SF (image +
builds/publish) passes; dungeon (gitops, no builds/publish) passes; **no test/example config has
a non-image mode with builds/publish**. A1's unknown-mode rule is likewise an error.

### A5 — Placement

New `func validateLifecycle(cfg *Config) []string`, called from `Validate()` alongside the other
`validate*` helpers. `Validate` runs at `loadResolved:224` — *after* preset resolution, so a
preset-supplied mode is already composed in. Self-contained; appends to `errs`. No new package,
no signature change (works purely off `*Config` for the safe subset — that's why the deferred
gitops/docker checks, which need `rootNode`, are out of scope for this pass).

### A6 — Tests / regression gate

- SF-shaped (image/empty, `builds`+`publish`) → **passes**.
- dungeon-shaped (`mode: gitops`, `gitops.backend: flux`, no builds/publish) → **passes** —
  *and* asserts the defaulted `gitops.backend`/`docker.backend` do **not** trip the check.
- `mode: gitpos` (typo) → **error** (today: silently runs the build body).
- gitops (or governance/docker) config carrying `builds:` → **error** (today: silent).
- empty mode + `builds`/`publish` → **passes** (empty ≡ image).
- image config carrying only the defaulted `gitops`/`docker` (no builds/publish) → **passes**
  (the regression guard — proves defaults don't false-positive).

---

## (B) Sketch — mode-table consolidation (the "give it brains" refactor)

**Goal:** collapse the ~4 duplicated CI switches + the `RunLifecycle` switch into **one source
of truth**, so mode knowledge lives in a table instead of copy-paste. **Behavior-preserving.**

### B1 — The table

```go
// src/config/mode.go  (config-level: validation + dispatch both read it)
// The five phases are universal — this table never picks a pipeline. It tells a
// phase BODY what to do for a given mode.
type LifecycleMode struct {
    Name           string
    IsReconcile    bool     // reconcile (not build) — gitops/governance/docker
    PhaseReconcile bool     // does perform's IN-BODY switch reconcile for this mode? gitops/governance: yes, docker: no (CLI-only)
    LegalBlocks    []string // A2's matrix, hoisted here
    // BackendFrom: nil ⇒ not a RunLifecycle backend (image = build body; governance = own subsystem)
    BackendFrom    func(*Config) (mode, name string)
}

var lifecycleModes = map[string]LifecycleMode{
    "image":      {Name:"image",      IsReconcile:false, PhaseReconcile:false, LegalBlocks:[]string{"builds","publish"}},
    "gitops":     {Name:"gitops",     IsReconcile:true,  PhaseReconcile:true,  LegalBlocks:[]string{"gitops"},
                   BackendFrom: func(c *Config)(string,string){ return "gitops", c.GitOps.Backend }},
    "docker":     {Name:"docker",     IsReconcile:true,  PhaseReconcile:false, LegalBlocks:[]string{"docker"},
                   BackendFrom: func(c *Config)(string,string){ return "docker", c.Docker.Backend }},
    "governance": {Name:"governance", IsReconcile:true,  PhaseReconcile:true,  LegalBlocks:[]string{"governance"}}, // own subsystem, BackendFrom nil
}

func ResolveMode(raw string) (LifecycleMode, bool) {
    m := strings.ToLower(strings.TrimSpace(raw))
    if m == "" { m = "image" }
    lm, ok := lifecycleModes[m]
    return lm, ok
}
```

Two flags, not one, because **today's in-perform `case "gitops","governance"` reconcile body =
`IsReconcile && PhaseReconcile`** — `docker` is `IsReconcile` but reconciles only via the CLI
`RunLifecycle` path, **not** inside the perform phase (asymmetry #2). Both flags are needed to
reproduce current behavior exactly; neither gates a *phase* — the five phases always run.

### B2 — Rewrites per call site (1:1 with today)

- **Perform (and sibling) phase bodies** (`ci_phases.go` ×4): `case "gitops","governance":` →
  `if m,_ := ResolveMode(cfg.Lifecycle.Mode); m.IsReconcile && m.PhaseReconcile { …reconcile body }`.
  (`docker` stays the build body in-phase — preserved by `PhaseReconcile:false`; the phase
  itself still runs for every mode.)
- **`RunLifecycle`** (`phase.go`): the `switch mode { gitops…; docker… }` →
  `if bf := m.BackendFrom; bf != nil { md,nm := bf(cfg); ResolveBackend(md,nm) }`. image and
  governance have `BackendFrom==nil` (image never reaches here; governance uses its own path).
- **`reconcileRunner`'s internal gitops-vs-governance branch** and the governance
  CI-source-inference hint: read `m.Name`/`m.IsReconcile` from the table instead of literals.
  Governance **stays its own subsystem** — the table records "reconcile mode recognized by CI"
  but carries no `LifecycleBackend`.
- **`validate.go` (A)**: the safe checks become `ResolveMode(mode).LegalBlocks` lookups — (A)
  and (B) share one table. Caveat carried from A2: the table's `gitops`/`docker` entries are
  only *enforceable* once presence detection exists (their content sentinels are defaulted), so
  (A) enforces the `builds`/`publish`/`governance` rows now and the table documents the rest
  until (B) — or a small presence-detection helper — lands. (A) seeds it, (B) completes it.

### B3 — Why A→B ordering is load-bearing

Today `default:` catches **empty and unknown** → image. `ResolveMode` maps empty→image but
returns `ok=false` for unknown. Because **(A) rejects unknown modes at validate time**, by the
time (B)'s `ResolveMode` runs in dispatch, mode is *guaranteed valid* — dispatch can trust it.
If (B) shipped without (A), `ResolveMode` would have to fall back unknown→image to preserve the
`default:` behavior, re-importing the footgun into the table. So (A) first isn't just tidy —
it's what lets (B) treat mode as trusted.

### B4 — Regression gate for (B)

Behavior test asserting the table reproduces every legacy switch for `{"", image, gitops,
docker, governance}`:
- in-perform reconcile set (`IsReconcile && PhaseReconcile`) == `{gitops, governance}` (docker excluded).
- `RunLifecycle` backend set (`BackendFrom != nil`) == `{gitops→GitOps.Backend, docker→Docker.Backend}`.
- SF (image) → perform builds; dungeon (gitops) → perform reconciles; docker CLI → backend;
  governance → own path. All five phases emit for every mode. Identical dispatch before/after,
  proven per mode.

## Follow-up — de-pollute lifecycle defaults (separate, low-priority)

`defaults()` (`config.go:286-288`) seeds `GitOps.Backend: flux` and the full
`DefaultDockerLifecycleConfig()` (`Backend: compose` + `Targets`/`IaC`/`Secrets`/`Drift`) onto
**every** config, cross-mode. Verified: those fields are read **only in-mode**
(`RunLifecycle` phase.go:74/76), so the cross-mode copies serve no purpose — an image config
carrying `docker.backend: compose` is phantom noise (visible in `config render`), and it's what
forces the gitops/docker presence checks to be deferred (their content sentinels can't tell
authored from default).

**Fix (when it's worth it):** apply mode-specific block defaults **conditionally** — only when
that mode is active — instead of unconditionally in `defaults()`. Docker's default carries
several sub-fields a real docker config relies on, so this needs **merge semantics** (a
post-decode step, not a base-swap), i.e. it touches the load path. Once done, the content
sentinels become valid and the **full** A2 matrix (incl. gitops/docker stray-block) is safe with
no presence plumbing. Low urgency: it's a cosmetic smell, not a correctness bug, and `docker` is
the non-preferred/vestigial mode — not worth bending the load path around today.

### Deferred (out of scope for both)

- Scalar flatten (`lifecycle: gitops`) — breakage risk > cosmetic gain; keep `lifecycle.mode`.
- **Multi-mode simultaneously** — not representable today (single scalar) and not in the
  proposal. The scalar conflates "builds an image?" with "which reconcile overlay?"; the table
  above is the natural place to relax the legal-block matrix *if* multi-mode is ever designed,
  but that's a separate design pass.
- Folding governance into `LifecycleBackend` — it's a distinct subsystem; leave it.
