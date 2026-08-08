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
another pipeline.

### Tag pipelines: intent tags run, state tags don't

Tags split by intent. A tag matching one of your declared `git.tags` sources is a
**release** — pushing `v1.2.3` spawns a pipeline that builds and publishes it. The tags
StageFreight pushes as bookkeeping (`latest`, `latest-dev`, `dev-<sha>` — pointers to
builds that already exist) match no release source and spawn nothing. The rendered
GitLab workflow rules carry your `git.tags` patterns verbatim (includes as `=~`,
`!`-excludes as `!~`), so the gate is whatever *your* repo declared — nothing about
StageFreight's own tag names is baked in.

Repos with **no `git.tags` block** get no gate: every tag spawns a pipeline (which is
also why recursive bookkeeping-tag pipelines persist until you declare tag sources).
The forge rule is an optimization at pipeline-creation time; the binary's release-policy
gate stays authoritative inside the pipeline — the one construct GitLab rules can't carry
(a tag pattern that isn't valid RE2, which the binary matches as a literal string)
degrades the whole gate back to all-tags-spawn rather than ever suppressing a release.

The gate is GitLab-only: Actions-family forges (GitHub, Gitea, Forgejo) filter tags by
glob, not regex, and their job guards have no regex operator — declared regex sources
can't be carried faithfully, so those skeletons run every tag pipeline and the binary's
release gate skips non-release tags in-stage.

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
- **Azure DevOps** — a PAT with **Code (read & write)**, authenticated over HTTP Basic. The
  client reads `AZURE_DEVOPS_TOKEN` (or `AZURE_DEVOPS_EXT_PAT`), falling back to the pipeline's
  built-in `SYSTEM_ACCESSTOKEN` — which Azure does **not** expose to jobs by default, so map it
  in: `env: { SYSTEM_ACCESSTOKEN: $(System.AccessToken) }` and grant the build service Contribute.
  Org/project/repo auto-detect from `SYSTEM_COLLECTIONURI` / `SYSTEM_TEAMPROJECT` /
  `BUILD_REPOSITORY_NAME`. Releases are `ErrNotSupported` (no native git-release — use tags).

## Forge status & runners

The [Integrations overview](README.md#forges) carries the full capability and live-validation
matrix. On runners:

- **GitLab** — self-hosted runner deployments (Compose: runner + buildkitd + DinD) are
  documented under [`gitlab/`](gitlab/README.md).
- **GitHub** — validated on **GitHub-hosted** runners. A self-hosted GitHub Actions runner
  guide is not written yet.
- **Azure DevOps** — a Kubernetes agent example lives under
  [`azuredevops/`](azuredevops/README.md).
