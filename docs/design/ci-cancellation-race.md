# CI cancellation race — narrate's own commit kills in-flight pipelines

**Status:** open bug. Observed repeatedly in production; costs builds silently.

## Symptom

A push produces no image. No job fails, no notification arrives, and the pipeline
is simply gone. Waiting longer never helps — the build was cancelled, not slowed.

Observed on `HomeLabHD/LinkStack`, 2026-09-14, twice in one afternoon:

| time | event |
|---|---|
| 13:35:10 | `7c34b90` pushed (application change) |
| 13:36:21 | `1416edf` *"docs: refresh generated badges"* pushed by the previous pipeline's narrate stage |
| — | no `dev-7c34b90` image ever published |

and again:

| time | event |
|---|---|
| 14:59:23 | `dev-a21f9d5` image published |
| 14:59:54 | `e8d4daf` pushed **and** `e40e8a1` narrate badge commit pushed |
| — | no `dev-e8d4daf` image; recovered only by a manual pipeline re-run |

The earlier build in each pair succeeded in roughly five minutes, so the silence
is not slowness.

## Cause

The emitter renders both of these (`src/ci/render/gitlab/emitter.go`):

```yaml
workflow:
  auto_cancel:
    on_new_commit: interruptible   # PipelineDefaults.CancelSuperseded
default:
  interruptible: true              # PipelineDefaults.Interruptible
```

Narrate pushes its generated-docs commit back to the branch that triggered the
pipeline, so **the pipeline races itself**. When that commit lands while a newer
push is still building, GitLab treats it as a new commit on the ref and cancels
the in-flight pipeline.

The existing self-skip rule does not prevent this:

```yaml
- if: '$CI_COMMIT_MESSAGE =~ /(?m)^Generated-By: StageFreight/ && $CI_PIPELINE_SOURCE == "push"'
  when: never
```

That rule is one-sided. It suppresses pipeline **creation** for narrate's commit,
which is what it was written for, but `auto_cancel.on_new_commit` acts on the
*arrival of a commit on the ref* and is indifferent to whether that commit
created a pipeline. So the badge commit starts nothing and cancels everything.

Because `interruptible: true` is a pipeline-wide default with no per-job
override, every stage is cancellable — including `publish`, which is the one
stage whose cancellation loses an artifact rather than just time.

## Recommended fix

Add a per-job `interruptible` override and render `publish` (at minimum) as
`interruptible: false`.

GitLab only auto-cancels jobs marked interruptible, and once a job with
`interruptible: false` has started, the pipeline stops being auto-cancellable at
all. That yields exactly the wanted property: cancellation stays available for
the cheap early stages where superseding is a genuine saving, and becomes
impossible from the moment a pipeline starts producing something.

This needs `Interruptible` on the job model rather than only on
`PipelineDefaults`, plus golden-file and fleet regeneration.

## Alternatives considered

- **Drop `auto_cancel`.** Fixes the race, but forfeits superseding entirely —
  every abandoned push then builds to completion.
- **Narrate pushes elsewhere** (orphan ref, or the artifact store). Removes the
  self-race at its root and is arguably the cleaner long-term shape, but it
  changes where generated docs live, which is a much larger contract change.
- **`[skip ci]` on narrate's commit.** Rejected previously for a documented
  reason — it is context-blind and also suppresses tags.

## Operator workaround until fixed

If no `dev-<sha>` appears in roughly twice the usual build time, check whether a
badge commit landed just after the push. If so the build was cancelled, not
failed, and no amount of waiting will produce it. Re-run the pipeline once the
branch is quiet — noting that a re-run builds the **branch tip**, which will be
the badge commit, not necessarily the commit intended.
