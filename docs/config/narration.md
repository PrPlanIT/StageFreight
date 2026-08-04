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
runs; narration renders them through **facts** — `{dotted.tokens}` that always
resolve and elide when their domain recorded nothing:

- Identity: `{project}` `{ref}` `{sha}` `{version}` `{commit_title}`
  `{pipeline_url}` `{duration}`
- Status: `{status}` `{status_icon}` `{status_verb}`, and on failure
  `{failure.subsystem}` / `{failure.reason}`
- Domains: `{publish.*}`, `{tests.*}`, `{security.*}`, `{changelog.*}`,
  `{retention.pruned}`, `{reconcile.*}` (gitops), `{ansible.*}` (host
  convergence)

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

## `llms:` + `type: llm` — AI as a stencil

AI narration is two declarations: a backend in the `llms:` library, and a
stencil of `type: llm` that composes an input body and sends it through that
backend. The library exists so endpoints and credentials never leak into
composition — an AI stencil says `llm: local` and stays pure text.

```yaml
llms:
  local:
    provider: ollama                 # openai | anthropic | claude-agent reserved
    url: http://ollama.example.com:11434
    model: deepseek-r1:1.5b

stencils:
  triage:
    type: llm
    llm: local
    body: |
      You are a CI triage assistant. In two sentences, explain the most likely
      cause of this failure and the first thing to check.

      {postmortem}

notifications:
  failure-triage:
    provider: ntfy
    url: https://ntfy.example.com/MyRepo-CI
    credentials: NTFY
    body: "{postmortem}\n\n{triage}"
    when:
      outcomes: [failure]
```

The `body:` of an llm stencil is the composed **input**: facts and stencil
embeds resolve first (so the model receives the real postmortem, the real
changelog), then the result goes to the backend and the response renders as the
stencil's output. Contracts that keep this safe and cheap:

- **Degrade to empty** — an unreachable backend or failed generation renders
  nothing (and the empty-body skip means no broken ping). The pipeline never
  fails because a model was down.
- **Per-run memoization** — a given llm stencil generates once per run, however
  many bodies embed it.
- **Reasoning-model hygiene** — `<think>…</think>` traces are stripped from
  output before rendering.
- **Dispatch-only, enforced** — `type: llm` stencils (and any text stencil that
  transitively embeds one) are rejected by validation in scribe file regions.
  The one deliberate exception: **release bodies** may embed AI stencils —
  what a release says is the author's editorial choice, and the release
  elements (`{release.changes}` etc.) flow into the stencil's input so a model
  can rewrite them.

--8<-- "docs/assets/modules/config-reference.md:llms"

## Where narration runs

The narrate phase is presentation only: it gathers recorded facts, renders the
arc body and any configured notifications, and dispatches. It holds no build
capabilities and no cluster credentials — by the time narration runs, the truth
is already recorded; narration just tells it.
