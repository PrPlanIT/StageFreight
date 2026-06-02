# Transport Rollout — First-Pipeline Validation Checklist

The publish-owns-distribution change (artifact identity carried perform → review
→ publish via the content store; publish promotes the reviewed bytes; perform no
longer pushes when transport is active) is **architecturally complete and proven
in isolation**, but its first appearance in the live GitLab pipeline is a
**bootstrap event, not a validation run.**

This document tells you exactly what must be true on run #1 vs run #2, so you can
tell bootstrap effects apart from real regressions.

---

## Why run #1 is not the new system

StageFreight dogfoods itself: the pipeline executes
`image: stagefreight:latest-dev`. That image is the **previously published
binary** — it does not contain the transport commits. So:

- **Run #1** executes the OLD binary against the new commit. Expect **legacy
  behavior**: perform still pushes (the legacy push path still exists and is the
  active path when no content store is wired by the old code). published.json is
  written the old way. This run's job is to **produce a new `latest-dev`**
  containing the transport code.
- **Run #2** (the pipeline triggered after run #1 publishes the new
  `latest-dev`) is the first run executed BY the new binary. This is where the
  transport behavior actually takes effect and where validation begins.

Do not interpret legacy behavior on run #1 as a regression. It is the bridge.

---

## Run #1 — bootstrap (expect legacy, verify the channel)

What MUST be true (these are not transport-specific — they prove the pipeline is
healthy enough to carry the new binary forward):

- [ ] perform succeeds and publishes a new `stagefreight:latest-dev`.
- [ ] The `.stagefreight/` artifact is produced by perform and is non-empty.
- [ ] publish + narrate complete (`allow_failure: true` means a soft failure
      won't block, but check the logs).

What is EXPECTED (not a bug) on run #1:

- perform pushes to the registry (legacy path — old binary has no transport).
- published.json is written by the old code path.
- No `.stagefreight/objects/` content store (old binary doesn't create it).

---

## Run #2 — first real transport run (validation begins)

Now the binary contains the transport code. Verify the new authority model:

### Perform retains, does NOT distribute
- [ ] perform logs show NO registry push (no `docker push`, no buildx `--push`
      for the image steps). For single-platform this means the load-then-push
      block is skipped; for multi-platform, `Push=false`.
- [ ] `.stagefreight/objects/` exists in the perform artifact and contains an OCI
      layout (`blobs/`, `index.json`, `oci-layout`) keyed by digest.

### Artifact survives to publish
- [ ] publish receives `.stagefreight/objects/` (it `needs: [perform, review]`
      and perform exports `paths: [.stagefreight/]`). The artifact must not have
      expired — `expire_in` is now `1 day` (was 2h); confirm publish ran within
      that window.

### Review scans carried bytes
- [ ] review (security scan) logs show it resolved the content-store layout
      ("scanning content-store artifact … carried from perform, re-hash
      verified"), NOT a registry pull. If it logged "falling back to … publication
      views", transport did not engage — investigate (likely a missing handle or
      a failed verify, NOT necessarily a regression — see fallback note below).

### Publish distributes + records
- [ ] publish logs show `promoted <name> → <ref> @ <digest> (digest preserved,
      no rebuild)`.
- [ ] The registry serves the **same digest** perform recorded (the promotion
      does a post-push self-verify; a mismatch fails loudly).
- [ ] published.json exists and records a `push` outcome with
      `ObservedBy: "promote"` and `status: success` — written BY publish.

---

## Known-correct fallbacks (do NOT treat as failures)

These paths are deliberate compatibility, not regressions:

- **A bare `docker build` CLI run (no lifecycle, no store)** uses the legacy
  perform-push path. Transport is a lifecycle feature; the standalone path is
  unchanged by design.
- **Review falling back to the publication-derived target** when there is no
  persistence handle or the layout fails verification. This is loud (it logs the
  reason) and is the correct safe behavior — but on run #2 it would indicate
  transport didn't engage, so investigate the *reason*, don't just accept it.

---

## The two failure modes most likely on the first real run

1. **Artifact expiry.** If publish runs > the `expire_in` window after perform
   (manual gate, queued runner, long review), `.stagefreight/objects/` is gone →
   publish finds a handle pointing at an absent path → verify fails → promotion
   skips. Mitigated to `1 day`; if your perform→publish gap can exceed that, raise
   it further in `src/ci/render/planner.go` (the generator), not the generated
   file.

2. **Double-push during transition.** If, on some intermediate run, BOTH the new
   transport path (publish promotes) AND a legacy push fire, an image could be
   pushed twice. This is harmless (promotion is digest-preserving and idempotent)
   but watch for it; it signals the perform-push suppression isn't fully active
   for that build shape.

---

## Steady state (what "done" looks like)

After run #2 validates clean, the steady state is:

```
perform : build → retain OCI layout to .stagefreight/objects/  (no push)
review  : resolve layout from CAS → re-hash → scan
publish : resolve layout from CAS → re-hash → promote (push, digest-preserved)
          → write published.json (ObservedBy: promote)
```

At that point the legacy perform-push and the review publication-fallback are
dead code on the lifecycle path, removable once transport is universal (a
separate decision: whether to forbid non-lifecycle image builds).
