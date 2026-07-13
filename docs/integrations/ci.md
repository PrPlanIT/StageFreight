# CI Integration

StageFreight **owns your pipeline document**. You don't hand-write CI YAML or copy a
per-forge skeleton — you *render* the pipeline from `.stagefreight.yml` and commit the
result. The generated file only translates forge-native context into `SF_CI_*` variables and
calls `stagefreight ci run <phase>`; all behavior lives in your config.

## Render the pipeline

```bash
stagefreight ci render <forge> --write
```

| Forge | `ci render` writes | Status |
|-------|--------------------|--------|
| `gitlab` | `.gitlab-ci.yml` | Live-validated — StageFreight builds itself here |
| `github` | `.github/workflows/stagefreight.yml` | Live-validated on GitHub-hosted runners |
| `gitea` | `.gitea/workflows/stagefreight.yml` | Render supported (shared Actions backend) |
| `forgejo` | `.forgejo/workflows/stagefreight.yml` | Render supported (shared Actions backend) |
| `azuredevops` | `azure-pipelines.yml` | Experimental |

- Default (no flag) prints to **stdout**; `--write` writes the file; `--check` verifies the
  committed file matches what would be rendered and exits `1` if it's stale — run it in CI so
  a config change can't silently drift from the pipeline.
- The rendered file is a **committed generated artifact** marked `DO NOT EDIT`. Regenerate it
  whenever `.stagefreight.yml` changes; never hand-edit it.

```bash
stagefreight ci render github --write   # writes .github/workflows/stagefreight.yml
git add .github/workflows/stagefreight.yml
stagefreight commit -t ci -m "render github pipeline"
```

## What the generated pipeline does

One **universal skeleton** serves every repo mode — StageFreight resolves the modality from
`lifecycle.mode`. Its jobs are the canonical lifecycle:

**audition → perform → review → publish → narrate**

— the same graph you see in [Screenshots](../screenshots.md). Each job:

1. Exports forge-native context into `SF_CI_*` environment variables.
2. Runs `stagefreight ci checkout` — materializes the workspace via go-git (no `git` binary
   required in the image).
3. Runs `stagefreight ci run <phase>` — the phase behavior comes entirely from your config.

### Loop prevention

StageFreight's own generated commits (badges, docs, dependency bumps) carry a
`Generated-By: StageFreight` trailer, and the rendered pipeline skips CI on those commits
(`when: never` on GitLab, an `if:` guard on GitHub) so an automated commit never triggers
another pipeline. Tags always run regardless of the trailer.

## Credentials

Registry auth uses the `credentials:` env-var prefix — see
[Concepts → Credentials](../config/concepts.md#credential-resolution). The **forge** token is
supplied per platform:

- **GitLab** — `GITLAB_TOKEN` with `api`, `read_repository`, `write_repository` scopes
  (create a **project or group access token**). Without it, StageFreight falls back to the
  job's built-in `CI_JOB_TOKEN`, which can push to the registry and read artifacts but
  **cannot create releases** (no `api` scope). Set `GITLAB_TOKEN` as a masked, protected
  variable.
- **GitHub** — the built-in `GITHUB_TOKEN` is used by default; set `GH_TOKEN` to a PAT to
  override when you need broader scope (e.g. pushing to another repo). The workflow requests
  `contents: write` on the jobs that commit back.

## Forge status & runners

The [Integrations overview](index.md#forges) carries the full capability and live-validation
matrix. On runners:

- **GitLab** — self-hosted runner deployments (Compose: runner + buildkitd + DinD) are
  documented under [`gitlab/`](gitlab/README.md).
- **GitHub** — validated on **GitHub-hosted** runners. A self-hosted GitHub Actions runner
  guide is not written yet.
- **Azure DevOps** — a Kubernetes agent example lives under
  [`azuredevops/`](azuredevops/README.md).
