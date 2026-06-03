# GitLab CI Components — deprecated as a driver

**Driving StageFreight via a GitLab CI Component is deprecated and will not be
developed further.** The files in this folder are retained only for reference and
for the existing Catalog publish; do not build new adoption around them.

The supported way to run StageFreight on GitLab — and every other forge — is to
render a native pipeline from your config:

```bash
stagefreight ci render gitlab --write
git add .gitlab-ci.yml && git commit
```

## Why this is deprecated

StageFreight exists to be **one config and one language that unifies CI across
forges** (Azure DevOps, Forgejo, GitHub, GitLab, Gitea). A GitLab CI Component
works against that in three ways:

1. **It would be a 1:1 replica of `.stagefreight.yml`.** A driver-component's
   `spec: inputs:` block has to mirror the StageFreight config, by hand, forever.
   Every config change becomes two edits, and the two drift the moment they
   disagree. Rendering reads `.stagefreight.yml` *directly* — one source of truth,
   no replica to maintain.
2. **It is GitLab-specific and non-portable.** The component format is a GitLab
   dialect. Adopting it re-locks a user to GitLab — the exact forge lock-in
   StageFreight is built to remove. The same `.stagefreight.yml` renders to all
   five forges unchanged; a component does not travel.
3. **Render already covers it.** `ci render gitlab` produces a native,
   audition-enforced `.gitlab-ci.yml`. The component adds nothing render doesn't,
   while adding a maintenance surface that fights the architecture.

## What *is* still supported

- **Rendering** the GitLab pipeline (and GitHub/Gitea/Forgejo/Azure) from one
  config — the canonical adoption path.
- **Publishing GitLab Catalog components via the StageFreight binary** — the
  general `gitlab-component` capability remains; StageFreight simply no longer
  ships *itself* as a component you include to run your build.

If GitLab Catalog discoverability matters, the right shape is a thin catalog stub
that points users at `stagefreight ci render gitlab` — not a full,
spec-replicating component that has to be kept in lockstep with every change.
