# StageFreight — CI Render Architecture

How a forge-neutral pipeline becomes a forge-native CI document. This layer has
one job and a strict ownership rule: **a forge is an identity, a renderer is a
mechanism, and the two never trade places.**

---

## The three layers

```
config (.stagefreight.yml)
        │  intent: lifecycle, builds, registries — no forge YAML
        ▼
ci/render/model.Pipeline           ── forge-neutral intent
        │  "what must run, in what order, with what routing"
        ▼
ci/render/<forge>.Emit             ── FORGE IDENTITY (one package per forge)
        │  github / gitlab / gitea / forgejo / azuredevops …
        ▼
ci/render/internal/<backend>       ── SERIALIZATION MECHANISM (private)
        │  e.g. actions — the GitHub Actions workflow wire format
        ▼
forge-native file at its native path
  .gitlab-ci.yml · .github/workflows/ · .gitea/workflows/ · .forgejo/workflows/
```

- **Model** is the leaf of the import graph. It encodes intent (jobs, stages,
  needs, routing, capabilities, policy) and knows nothing about any forge.
- **Forge emitter** (`ci/render/<forge>`) is the public identity. There is exactly
  one package per supported forge. It owns that forge's lowering decisions and is
  the only place a forge-specific special case may live.
- **Serialization backend** (`ci/render/internal/<backend>`) is private mechanism.
  It is a wire-format writer (e.g. the Actions workflow format) parameterized by a
  dialect. It is `internal/`, so nothing outside `ci/render` can import it, and it
  never appears in a user-facing surface.

---

## Invariants

### 1. One forge, one package, one identity
> Every supported forge has its own `ci/render/<forge>` package and its own entry
> in `Emit` and `ForgeTarget`. No forge is "an alias of" another in any
> user-visible or architectural sense.

GitHub, Gitea, and Forgejo today share the Actions wire format. That is an
*implementation* fact, confined to `internal/`. Each still has its own package,
its own `provider:` identity (`SF_CI_PROVIDER`), its own output path, and its own
API client. A Gitea user sees Gitea; a Forgejo user sees Forgejo.

### 2. Serialization backends are private and never escape `ci/render`
> A backend lives under `ci/render/internal/`. Forge emitters import it; nothing
> else does, and it is never named in CLI help, config, docs, or output.

This is what keeps "we reuse the Actions writer" from becoming a user-facing
concept. The boundary is enforced by Go's `internal/` rule, not by convention.

### 3. Output is byte-deterministic and golden-tested
> Identical model + identical forge → identical bytes. Every forge has a golden
> render test. `ci render --check` is exact-match; drift is a hard failure.

Users commit the generated file and the pipeline self-checks it (`audition`).
That only works if rendering is a pure, stable function. Golden tests per forge
are the contract; the backend may be refactored freely as long as the goldens
hold.

### 4. Identity decides mechanism, never the reverse
> The user expresses a forge; StageFreight derives backend, OIDC strategy, output
> path, and API client. The mechanism is never selected by the user.

```
forge: forgejo   →   backend = actions
                     oidc     = forgejo
                     path     = .forgejo/workflows/stagefreight.yml
                     client   = forgejo
```

Encode intent, hide mechanism — the same rule the rest of StageFreight follows.

---

## Why provider ownership at the boundary (not a shared "actions" forge)

Divergence is inevitable, and it lands per-provider:
- GitHub: reusable workflows, environments, fine-grained `permissions`.
- Gitea: `act_runner` label/scheduling quirks.
- Forgejo: its own OIDC behavior, package-registry and federation features.

When the first special case arrives, the forge that needs it edits its own
`ci/render/<forge>` package — passing a different dialect, post-processing the
backend's output, or dropping to a bespoke emitter entirely — **without touching
its siblings.** Had the three been collapsed into one "actions" target, the first
divergence would force a refactor of a package other forges depend on. One
package per forge makes divergence a local edit.

---

## Adding a forge

1. Create `ci/render/<forge>/emitter.go` exposing `Emit(model.Pipeline) ([]byte, error)`.
2. Choose its serialization: reuse an existing `internal/` backend with a dialect,
   or write a new one (Azure DevOps will need its own).
3. Add a golden test: `ci/render/<forge>/testdata/<forge>.golden.yml`.
4. Wire it: a case in `render.Emit`, a path in `render.ForgeTarget`, an entry in
   `SupportedForges`, and detection in `forge/detect.go`.
5. Add its API client under `forge/<forge>` (releases, PRs, OIDC) — recycling a
   compatible client internally is fine, but the identity is first-class.

The CLI never changes: `stagefreight ci render <forge>` already takes any forge.
No new verbs, no backend names — ever.
