# Narration & Notifications

Narration is the *message* half of StageFreight's audience text: the run
summary printed at the end of a pipeline, the notification that lands on your
phone, and the optional AI-processed retelling of either. It shares one grammar
with [Stencils & Scribe](scribe.md) — freeform bodies of facts and `{stencil}`
embeds — but where scribe places rendered text into *files*, narration
dispatches it as *messages*. That difference carries a hard rule: AI output is
**dispatch-only** (stdout cards, notifications, release bodies by the author's
choice) and can never enter a scribe file region — the committed record stays
deterministic.

## The run story: facts, union bodies, elision

Every subsystem records its outcome and metrics into the pipeline state as it
runs; narration renders them through **facts** — tokens that always resolve and
elide when their domain recorded nothing. The complete vocabulary:

**Identity & status** (bare names, usable anywhere):

| Fact | Renders |
|---|---|
| `{project}` | project name (from the git remote) |
| `{modality}` | the run's lifecycle modality |
| `{ref}` | branch or tag the run built |
| `{sha}` / `{version}` | the run's recorded identity (short SHA / version) |
| `{commit_title}` | HEAD commit title |
| `{pipeline_url}` | forge pipeline URL (tap-through target) |
| `{duration}` | elapsed from the run's first state write ("3m 12s") |
| `{status}` / `{status_icon}` / `{status_verb}` | pipeline outcome as word / icon / verb |

**Domain facts** (`{domain.key}` — one domain per subsystem; every metric
elides when its domain recorded nothing):

| Domain | Keys |
|---|---|
| `failure.` | `subsystem` · `reason` (the first pipeline-failing subsystem) |
| `publish.` | `tags` · `registries` |
| `tests.` | `total` · `passed` · `coverage` |
| `security.` | `blocking` · `critical` · `high` · `medium` · `low` · `total` · `sbom` · `blocking_list` |
| `changelog.` | `count` · `range` |
| `reconcile.` (gitops) | `total` · `succeeded` · `failed` · `declined` · `backend` · `units` · `cluster` · `failures` |
| `ansible.` (hosts) | `total` · `converged` · `changed` · `unreachable` · `failed` |
| `retention.` | `pruned` |

Every recorded domain additionally serves `{domain.outcome}` and
`{domain.reason}` — the universal subsystem keys.

**Producers and arcs** (shipped stencils, embeddable as `{id}` and overridable
by shadowing): `{summary}` and `{postmortem}` (the arc bodies), plus the four
looping producers `{changelog}`, `{failures}`, `{vulns}`, `{artifacts}` —
self-bounding row lists that render nothing when empty.

**Everything else falls through to the template leaf-pass**: `{base}`,
`{major}`/`{minor}`/`{patch}`, `{env:NAME}`, `{var:name}`, `{project.*}`, and
the date/counter vocabulary — see [Concepts](concepts.md). Resolution order per
token: caller context (release elements) → recorded facts → your `stencils:` →
shipped stencils → the leaf-pass; unknown tokens stay visibly literal, so a
typo shows itself.

This vocabulary is ratcheted: the registry lives beside the fact resolver
(`src/cistate/vocabulary.go`) and a docs test fails any fact that ships
undocumented.

The shipped `summary` (success arc) and `postmortem` (failure arc) are **union
bodies**: one template holding every modality's lines, composed per line — a
line whose facts all resolved empty drops out, label and all. An image repo, a
gitops repo, a host-converging repo, or one doing all three renders coherently
from the same body:

```
Shipped {publish.tags} → {publish.registries}
Converged {reconcile.succeeded}/{reconcile.total} {reconcile.units} on {reconcile.cluster}
Converged {ansible.converged}/{ansible.total} nodes · {ansible.changed} changed
Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage
```

Overriding is shadowing: declare a stencil named `summary` (or `postmortem`)
and your body replaces the shipped one everywhere it is embedded. Adding a
modality never means a new template — it means new facts and new lines.

--8<-- "docs/assets/modules/config-reference.md:narrate"

## `notifications:` — messages that dispatch themselves

The top-level `notifications:` block is an id → entry map where endpoint and
message are **fused** — a notification is one thought, not a router plus a
template. Subject, body, and click are freeform stencil bodies; omitted, they
default to the shipped subject (`{project} {ref} — {status} in {duration}`)
and the run's arc body (summary on success, postmortem on failure).

```yaml
notifications:
  phone:
    provider: ntfy
    url: https://ntfy.example.com/MyRepo-CI
    credentials: NTFY                # → NTFY_TOKEN (Authorization: Bearer)
    priority: high
    tags: [rotating_light]
    when:
      outcomes: [failure]            # composes with branches:/events:/git_tags:
      branches: [main]
    max_length: 4096                 # trim at a line boundary; pipeline link survives

  recap:
    provider: ntfy
    url: https://ntfy.example.com/MyRepo-CI
    credentials: NTFY
    body: "{summary}"
    when:
      outcomes: [success]
```

Dispatch rules worth knowing:

- **`when:` is the one grammar** — the same `events`/`branches`/`git_tags`
  conditions used everywhere, extended with the `outcomes:` dimension
  (`success` | `failure` | `warning`). Omitted `when:` = always.
- **Empty bodies skip** — a notification whose rendered body is empty (every
  line elided) does not ping anyone.
- **`max_length` trims at line boundaries** and always preserves the
  pipeline-link line, so tap-through survives truncation.
- Full ntfy header vocabulary: `priority`, `tags` (emoji), `click`, `attach`,
  `actions`, `markdown`, `email`.

--8<-- "docs/assets/modules/config-reference.md:notifications"

## Dispatching AI narration

AI lives elsewhere in the config — model endpoints in the
[`llms:` library](identity.md) (Identity & Connectivity, a sibling of forges
and registries) and the [`type: llm` stencil](scribe.md) that composes input
and consumes a backend (Stencils & Scribe). Narration is where their output
DISPATCHES: embed an AI stencil in a notification body like any other stencil.

```yaml
notifications:
  failure-triage:
    provider: ntfy
    url: https://ntfy.example.com/MyRepo-CI
    credentials: NTFY
    body: "{postmortem}\n\n{triage}"     # triage is a type: llm stencil
    when:
      outcomes: [failure]
```

The dispatch-side behaviors that matter here: an unreachable backend renders
nothing, and the **empty-body skip** means no broken ping goes out; generation
is memoized per run, so a subject and body embedding the same AI stencil cost
one generation. AI output is dispatch-only — these bodies and release notes,
never scribe file regions.

## Where narration runs

The narrate phase is presentation only: it gathers recorded facts, renders the
arc body and any configured notifications, and dispatches. It holds no build
capabilities and no cluster credentials — by the time narration runs, the truth
is already recorded; narration just tells it.
