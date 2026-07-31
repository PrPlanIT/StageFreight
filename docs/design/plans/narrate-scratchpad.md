# Narrate: the storyteller (scratchpad)

> Working design from a live session. narrate's real charter: **tell the story of the run.**
> The theatre metaphor paying itself off — the phases (`audition · perform · review · publish`)
> are a play; **narrate is the narrator** who steps forward at the end and tells the audience
> what just happened. This is the original StageFreight (Ansible-era) dream, now buildable in Go.
>
> Companion (higher-level): `narrate-summary-ai-notifications.md`. Issues: #19 (notifications),
> #29 (ollama/hosted AI). This doc is the concrete config + model.

## Thesis

narrate already has the run's structured truth (`cistate`: modality, every phase/subsystem
outcome + reason, artifacts, releases, retention, mirror sync) **plus the run's changelog** —
the conventional-commit notes SF already generates for the release. It renders that truth into
a **story** and emits it through any stage door (stdout, ntfy, webhook, release body).

**The default is the *detailed* one — the source of truth.** It is *complete* (a human knows
everything within reason) and *dual-legible*: it reads well raw AND parses cleanly for an AI to
summarize. Terser, purpose-shaped outputs (dev push, community release note) are **projections**
of it — native templates or an AI lens — never competing defaults. This detailed default is also
the run's persisted **"last summary"**: the record narrate renders in its structured output block
and stores alongside the other run artifacts. Fidelity is a dial (`detail:`), not a rewrite.

### Repeatability (the invariant)

`render(cistate, changelog, template) → summary` is a **pure, deterministic function** — no
`time.Now()`, no map-iteration nondeterminism, no ambient state. Same run, same summary,
byte-for-byte. This is the exact discipline the badge subset already enforces (the zeroed
head-timestamp fix), and it gets the same guard: **a determinism test** that renders a fixed
`cistate` twice and asserts identical bytes. Missing fields degrade gracefully — a beat with no
data renders nothing, never a broken template. "Spliced" means *structured-data → typed-beats →
template*, not string-munging scraped log text.

## The model — two spaces, two edges

- **`summaries`** (CONTENT) — keyed map. `default` is SF-generated (a modality-specific template,
  embedded + forkable). A derived summary is `kind: ollama` — because *what ollama produces is a
  summary*. ollama is a **transform**, never a destination.
- **`targets`** (DESTINATIONS) — keyed map, `kind: stdout | ntfy | webhook | release`. Each
  *emits* a summary. `stdout` is a peer target (the front-row seat), not a special case.
- **`from:`** — the pipe. Default is the `default` summary; set it to another entry's id to
  consume *that* entry's output. So `summary → ntfy` and `summary → ollama → ntfy` are the same
  mechanism, one different `from`. (Mirrors `builds`' `stage.from`.)
- **`on:`** — the case: `failure | success | always` (later: `release`, `drift`).

Naming is deliberate: `summaries`/`targets` are SF's existing idiom (`targets` = kinded
destinations, like publish `targets`). Rejected along the way — and why: `notify.ntfy:` (closed,
hard-codes the world), a `[ {kind} ]` list (nothing in SF is a list of kind-objects), an `ai:`
block ("ai" is a bad tag; AI is not a stage name), `targets` *holding ollama* (a transform is not
a destination), `sinks` (plumbing jargon). Landing: **keyed map + `kind`, ollama lives in
`summaries` because its output is a summary.**

## Config surface

**Minimal — the default story is already yours.** No `summaries` block → narrate renders the
**modality's embedded default story** and prints it to stdout. One target adds a door:

```yaml
narrate:
  targets:
    push: { kind: ntfy, url: "https://ntfy.pcfae.com/sf-ci", token: "{env:NTFY_TOKEN}", on: failure }
```

**Full — voice + multiple doors:**

```yaml
narrate:
  summaries:
    default: { template: docs/ci/story.md }        # your fork — OMIT to use SF's embedded modality default
    briefed:                                        # an AI lens on the SAME truth
      kind: ollama
      from: default
      url: http://glicynia:11434
      model: gemma3
      prompt: "Rewrite as a 2-line push: lead with pass/fail, then the one thing that matters."
      stream: false

  targets:
    console: { kind: stdout, from: briefed }        # narrate in the AI voice
    push:
      kind: ntfy
      url: https://ntfy.pcfae.com/Ad_Arbitorium-GitOps
      token: "{env:NTFY_TOKEN}"
      from: briefed
      title: "StageFreight 🔔"
      tags: [rotating_light]                        # list → comma-joined Tags header
      priority: high
      click: "{pipeline_url}"
      on: failure
    release-notes: { kind: release, from: default, on: success }   # the SAME story becomes the release body
    chat:          { kind: webhook, url: "{env:SLACK_WEBHOOK}", from: default, on: always }
```

## The story is a document with vars + composable beats

The default's spine is **verdict → what changed → how it ran → what shipped → housekeeping**.
Changes is the *substance* (a run summary without the changelog is a receipt without the
purchase); the CI mechanics are receipts under it. `docs/ci/story.md`:

```markdown
# {project} — {status}
**{modality}** · {ref} · `{sha}` · {version} · [pipeline]({pipeline_url})

## Outcome
{> verdict}                   <!-- one line: passed/failed + the apex (what shipped, or what broke) -->

## Changes {> change_range}   <!-- "(12 commits since dev-b7a7f50)" -->
{> changes}                   <!-- the release changelog, conventional-commit grouped: Features/Fixes/… -->

{#if failed}
## What broke
{> failures}                  <!-- failing subsystems + reasons + any auto-remediation -->
{#else}
## Phases
{> acts}                      <!-- perform ✓ · review ✓ · publish ✓, each with its one detail -->
{/if}

## Artifacts & release
{> shipped}                   <!-- image@digest → tags across registries; release + assets/links -->

## Housekeeping
{> coda}                      <!-- retention pruned, mirror synced -->
```

- **Changes reuses the release changelog** SF already generates from conventional commits —
  generated once, shared by summary / release body / community projection. It's the human
  substance and the AI's raw material both.
- Vars pull from `cistate`. `{> beat}` are **composable partials** — customizing = reorder/reword
  beats, not rebuild a string (like scribe content items, but for narrative).
- `{#if failed}` makes the story **branch triumph-vs-post-mortem** — narrate reads the room. On
  failure, Changes still shows (what this run *would* have shipped, and where it broke — often
  exactly enough to triage without opening the forge).
- **`sf narrate template <modality> > docs/ci/story.md`** scaffolds SF's embedded default so you
  fork from the real thing. Docs say: "this is internal — copy it, make it yours."

## Projections — the terse/purpose versions derive from the detailed default

The detailed default is the source; everything else reads *from* it:

- **embed directly** — a target that wants the record sends the default as-is (release body loves it).
- **native projection** — a template that lifts the apex for a purpose:
  - *dev push* (ntfy/matrix, esp. `on: failure`): `🚨 publish failed: stale SHA — a newer run will
    ship.` The utility bar is **act without opening the forge**: the failing subsystem + reason.
  - *community release* (discord/telegram, `on: success`+release): leads with the **Features** list
    from Changes + how to get it — presentable, feature-first, zero CI internals.
- **AI projection** — `from: default, kind: ollama, prompt: "…"` re-renders at any fidelity/voice.

Tone follows purpose: dev push is spare/technical, community note is clean/presentable, the default
is complete/neutral. Per-target is **density + voice, not a different truth** — all one source.

## Default shapes per modality (embedded, forkable)

- **image** → build-and-ship: "built `stagefreight`, scanned clean, shipped 6 tags, cut `dev-cab91d0`."
- **gitops** → reconciliation: "converged 14 kustomizations against `dungeon`, 1 drifted, healed."
- **governance** → audit: "swept 3 clusters, 2 policies flagged, 0 blocking."

## Kinds

**summary kinds:** `template` (default; a doc with vars — inline string or `{template: <path>}`),
`ollama` (transform: POST `{url}/api/generate` with `model`/`prompt`(= `from` summary + prompt)/`stream`,
extract `.response`). Later: `openai`/`anthropic` (hosted, token auth).

**target kinds:** `stdout` (print), `ntfy` (POST url; headers: `title`→Title, `tags`→Tags,
`priority`→Priority, `click`→Click, `attach`→Attach, `actions`→Actions, `markdown`→Markdown,
`email`→Email, `token`→`Authorization: Bearer`), `webhook` (POST the summary anywhere —
slack/discord/matrix escape hatch), `release` (set the release body — the story IS the release
notes; uses UpdateReleaseNotes). All: `from:` + `on:`. New kinds = engine additions, **zero shape
change**.

These collapse the old hand-rolled GitLab CI components (bash `curl`+`jq`, alpine install per run,
un-pipeable) into one typed block that pipes.

## Magic (the enrichments)

- **AI is a lens, not a rewrite** — same truth, different register per target (terse push vs warm
  release notes vs blame-free post-mortem). `from:` picks the voice.
- **Story branches on outcome** — `{#if failed}` swaps the arc.
- **Durable artifact** — the rendered story is the release body / a `NARRATIVE.md`, not just a ping.
- **Continuity (someday)** — "recovered from last run — 3rd green in a row."
- **stdout is the front-row seat** — the CI log reads like a story, not a wall of boxes.

## Build order (skeleton first — real, not vapor)

1. Template engine + `cistate` var vocabulary + composable `{> beats}` + `{#if}` + the
   **determinism test** (render a fixed `cistate` twice → identical bytes; the badge discipline).
2. The **Changes beat** — bind to the release changelog SF already generates (one source, shared
   by summary / release body / community projection).
3. Embedded `image`-modality **detailed default** (Changes-first spine) + the `detail:` dial.
4. `stdout` target → `ci run narrate` narrates a real run today, inside a structured output block;
   the rendered default persists as the run's **"last summary."**
5. `ntfy` target (full header set).
6. `ollama` summary kind (the lens) + native purpose projections (dev push, community release).
7. `release` + `webhook` targets; `gitops`/`governance` default stories; `sf narrate template` scaffold.

## Progressive disclosure (the invariant)

Omit everything → SF narrates the modality default to stdout. Add a target line → another door.
Point `default` at a file → your story. Add `kind: ollama` → a new voice. Never a cliff.

---

## UPDATE — the stencils reframe (composition language as a first-class primitive)

> A later session reframed the whole thing. Sections above are the narrate-scoped
> origin; the model below supersedes them where they conflict. The driver: **audience-
> facing text is SF's pickiest surface**, so *everywhere* SF mutates text a human reads
> as the project's voice must offer the SAME escape hatch — one consistent lever for the
> vibe. That's not reuse-for-DRY (narrow — badges don't travel); it's **consistency of
> control.**

**One composition language, not narrate's.** A stencil is a reusable pattern SF STAMPS,
filling it per run: freeform markdown you author with `{element}` embeds. Every element —
a scalar fact, a badge, a composed stencil — is the same `{name}` embed; the sole control
form is `{#if cond}…{#else}…{/if}`. This supersedes the old `{var}` vs `{> beat}` split and
the `summaries`/`targets` two-space model above.

**Facts vs framing (the design line).** SF owns the FACTS — `{ci.*}`, `{changelog}`,
`{image.*}`, `{project.*}` — structured and unfakeable. The author owns the FRAMING — the
prose/order/voice around them. Defaults live underneath: omit a stencil → SF stamps its
embedded default; author one → you've seized the pen. Always available, never required.

**Nouns & tiers.**
- **`stencils:`** — top-level shared library of named text elements (lifts `scribe.content`
  out; same tier as `builds:`/`repos:`). Noun chosen over `content`/`macros`/`lines`/
  `templates` (the last collides with SF's internal "template"/version-format meaning).
- **`notifications:`** — top-level endpoint library (ntfy/webhook/matrix/email by id),
  mirroring `registries:`. Discoverable plain name; reusable across events.
- **Consumers differ only by destination**: `scribe` lands text in REPO FILES (a commit);
  `narrate` lands it in NOTIFICATIONS (stdout / referenced `notifications:` endpoints /
  release body). `narrate` owns routing (what×where×when); `stdout` is a narrate target,
  not a notification endpoint. `props` demotes from a user-facing render-class to an
  internal built-in catalogue — every element (badge/shield/inventory/producer/text) is an
  equal `stencils:` entry.
- **AI is a stencil kind, not a key.** `kind: ollama` (`from:` a stencil + `prompt`) resolves
  to text like any element — no `ai:`/`text-gen:` block. An ollama *server* lifts into an
  endpoint library only if server-reuse actually appears.

**Determinism boundary (ollama is non-idempotent).**
- The **canonical story** (facts + framing) is pure and reproducible — the ONLY thing
  persisted as the "last summary" or compared/committed. Guarded by the determinism test.
- An **ollama stencil is dispatch-only**: fine to SEND to ntfy/matrix (ephemeral), NEVER the
  stored/committed artifact — else it re-creates the badge no-op-commit churn. **AI feeds
  targets, never the record.** Treated as impure by construction (temp 0/seed narrows but
  never guarantees byte-stability across a model bump).
- **Resolve-once-per-render memoization**: an element referenced twice yields the SAME text
  within a render (one value per run, stable wherever it appears; fresh across runs). Also
  stops expensive facts being recomputed per reference.

**Migration is split by cost (no building on spec).** Cheap + done now: the generic
`stencil` engine + narrate as its first native consumer. Deferred: lifting `scribe.content`
to `stencils:` — do it when a *second consumer actually shares an element* (almost certainly
the changelog), so the migration pays for itself.

## Built so far (this reframe)

- **`src/stencil/`** — the shared engine: `Render(tmpl, Env{Resolve, Cond})`, single
  `{element}` + `{#if}`, unknown-token-literal, graceful-empty, blank-line collapse,
  memoized-per-render, pure/deterministic. Tests green (units + determinism + memo + nil-safe).
- **`src/narrate/`** — first consumer: `Input` → `stencil.Env` (facts + composed elements),
  `RenderStory`, embedded `image` default stencil (`templates/image.md`, Changes-first spine),
  outcome-inverted. Tests green (shape + determinism + inversion + graceful-empty).
- **`src/release/`** — exported `Categorize` so narrate's Changes uses ONE changelog source.

## Next

4. Narrate runner (`ci run narrate`): gather `Input` from `cistate.ReadState` + changelog
   (`release.PreviousReleaseTag`/`ParseCommits`/`Categorize`) → render → stdout; persist the
   **deterministic** story as the "last summary."
5. `notifications:` endpoint library + `ntfy` target (references endpoints; full header set).
6. `ollama` stencil kind (dispatch-only, impure).
7. `release`/`webhook` targets; `gitops`/`governance` default stencils; `sf narrate template`
   scaffold; then the `scribe.content` → `stencils:` lift when the changelog forces it.
