# Narrate: run summary → AI curation → notifications

> **Status:** direction (not built). Supersedes the "dissolve docs into narrate" framing:
> badges + generated docs are **published artifacts** and belong to the **publish**
> domain, not narrate. Narrate is *truth presentation*, and its real charter is the
> post-run experience below — summary, optional AI curation, and notifications.

## Thesis

Narrate is the phase that turns a completed run into **communication**, for two
distinct audiences with distinct needs:

1. **You (human).** "This is what I want the run presented to *me* like." A
   templatable summary — the run's outcome, curated and formatted the way the
   maintainer wants to read it.
2. **An AI.** "This is what I want the AI to *process* it like." The same run
   state, handed to a model (local or via API) with a free-form prompt, so the
   output can be reframed, triaged, explained, or turned into a next action.

Then those outputs fan out as **notifications**.

## Three stages

### 1. Summary (templatable)

The run's truth (phase outcomes, artifacts, releases, diffs, timings) is already
computed. Narrate lets you **template** it into the presentation you want — mix
free-form prose with resolved run values, the same way scribe templates a badge
value. This is the human-facing artifact.

### 2. AI curation (optional, local or API)

A configurable step that feeds run context to a model with a **free-form prompt**,
so you control *how* it's processed. Curation is context-shaping, not a black box:

- **Query before and after** — a prompt that runs before the summary is composed
  (to shape what context is gathered/curated) and one after (to react to the
  composed summary). You mix your prompt into anything we already allow templating
  into the summary.
- Local model or API-backed, operator's choice.
- Output is itself templatable and can feed notifications.

The split is deliberate: *what I want presented to me* (stage 1) is a different
artifact from *what I want the AI to make of it* (stage 2). Neither is forced.

### 3. Notifications

Template the notification body from **either** the AI output **or** the templated
summary, attach icons/severity, and dispatch. Tiered by how much we own:

- **First-class (we script these well):**
  - **ntfy** — rich: title, tags/icons, priority, click actions, attachments.
  - **generic SMTP** — email the summary.
  - **HTTP web push** — generic webhook/push.
- **Common patterns (maybe, if worth it):** a handful of the usual suspects.
- **Escape hatch (everything else):** a generic mechanism so people can wire
  Matrix / Discord / Telegram / Slack / whatever themselves — we don't chase an
  integration zoo; we give a clean seam.

## Why this is narrate, not publish

Publish *distributes artifacts* (images, packages, pages, releases, badges, docs —
things that must physically land on a forge/registry). Narrate *presents truth*
about what happened — it reads prior-phase state and communicates it, and never
mutates distribution targets. Summaries, AI curation, and notifications are
presentation/communication, so they're narrate's natural home. Moving badges/docs
out to publish leaves narrate a clean stage for exactly this.

## Open questions

- Config surface for the two-audience split (a `summary:` template + an `ai:`
  block with before/after prompts + a `notify:` list of channels).
- Where AI credentials/config live and how local-vs-API is selected.
- Notification channel schema (icons/severity mapping) and the escape-hatch shape.
- Whether AI curation can gate/annotate (advisory only vs. can influence exit).

## Related

- Publish-domain move: badges/docs/commit relocate from narrate to publish (this
  is the enabling cleanup).
- #46 themeable rendering, #47 QR codes — presentation surfaces narrate can drive.
