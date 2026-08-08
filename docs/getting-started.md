# Getting Started

StageFreight owns your CI pipeline: you don't hand-write CI YAML, you render it from one
`.stagefreight.yml` and commit the result. This page covers how the pipeline works, how to
get it running, and real configs to copy.

## How it works — the five-job pipeline

Every repo, on every forge, renders the same universal skeleton — StageFreight resolves the
modality from `lifecycle.mode`. The jobs are the canonical lifecycle:

**audition → perform → review → publish → narrate**

— the same graph you see in [Screenshots](screenshots.md). Each job:

1. Exports forge-native context into `SF_CI_*` environment variables.
2. Runs `stagefreight ci checkout` — materializes the workspace via go-git (no `git` binary
   required in the image).
3. Runs `stagefreight ci run <phase>` — the phase behavior comes entirely from your config.

The generated CI file is pure transport; all behavior lives in `.stagefreight.yml`.

## Get it running

1. **Render the pipeline** and commit it — a generated artifact marked `DO NOT EDIT`,
   regenerated on every config change, never hand-edited:

    ```bash
    stagefreight ci render <forge> --write     # gitlab · github · gitea · forgejo · azuredevops
    git add <the rendered file>
    stagefreight commit -t ci -m "render pipeline"
    ```

    No flag prints to stdout; `--check` fails if the committed file is stale — run it in CI so
    config can't silently drift from the pipeline. Each forge's output path and token live on
    its [integration page](integrations/README.md).

2. **Have a runner.** GitHub Actions runs on GitHub-hosted runners natively — nothing to
   stand up. GitLab and other self-hosted setups need a runner with Docker + BuildKit; the
   [runner deployments](integrations/README.md) carry ready compose stacks.

3. **Push** (or **tag**), and the five jobs run. Forge tokens and registry credentials resolve
   from CI variables at run time — see [Concepts → Credentials](config/concepts.md#credential-resolution).

## Pipeline behavior

### Loop prevention

StageFreight's own generated commits (badges, docs, dependency bumps) carry a
`Generated-By: StageFreight` trailer, and the rendered pipeline skips CI on those commits
(`when: never` on GitLab, an `if:` guard on Actions) so an automated commit never triggers
another pipeline.

### Tag pipelines: intent tags run, state tags don't

Tags split by intent. A tag matching one of your declared `git.tags` sources is a
**release** — pushing `v1.2.3` spawns a pipeline that builds and publishes it. The tags
StageFreight pushes as bookkeeping (`latest`, `latest-dev`, `dev-<sha>` — pointers to builds
that already exist) match no release source and spawn nothing. The rendered GitLab workflow
rules carry your `git.tags` patterns verbatim (includes as `=~`, `!`-excludes as `!~`), so the
gate is whatever *your* repo declared.

Repos with **no `git.tags` block** get no gate: every tag spawns a pipeline. The forge rule is
an optimization at pipeline-creation time; the binary's release-policy gate stays authoritative
inside the pipeline — a tag pattern that isn't valid RE2 degrades the whole gate back to
all-tags-spawn rather than ever suppressing a release. The gate is GitLab-only: Actions-family
forges filter tags by glob, not regex, so those skeletons run every tag pipeline and the
binary's release gate skips non-release tags in-stage.

## Pick a scenario to copy

The fastest way to learn StageFreight is to read a **real `.stagefreight.yml` that's actually
running.** Open the one closest to yours, copy the shape, then reach for
[Configuration](config/index.md) when you want a knob it doesn't show.

| Scenario | Archetype | Knobs it demonstrates | Live config |
|---|---|---|---|
| **Container app (full lifecycle)** | Dockerfile image, dev + stable channels | `builds`, `kind: registry`, `kind: metadata`, `scribe` badges, retention | [DD-UI](https://github.com/PrPlanIT/DD-UI/blob/main/.stagefreight.yml) |
| **CLI / binary distribution** | Go binary + image + downloadable archives | `kind: binary`, `kind: binary-archive`, `kind: release` with checksums | [HASteward](https://github.com/PrPlanIT/HASteward/blob/main/.stagefreight.yml) · [Dragonfly](https://github.com/HomeLabHD/dragonfly/blob/main/.stagefreight.yml) · [Jetpack](https://github.com/HomeLabHD/jetpack/blob/main/.stagefreight.yml) |
| **GitOps repo** | Flux manifest validation, no image build | `lifecycle: { mode: gitops }`, cluster auth | [Dungeon](https://github.com/SoFMeRight/Dungeon/blob/main/.stagefreight.yml) |
| **Governance / control repo** | Policy reconciliation across repos | `lifecycle: { mode: governance }` | [MaintenancePolicy](https://github.com/PrPlanIT/MaintenancePolicy/blob/main/.stagefreight.yml) |
| **Static site → Cloudflare Pages** | Docs site built + deployed on release | `kind: command` (mkdocs build), `kind: pages` (Cloudflare) | [StageFreight](https://github.com/PrPlanIT/StageFreight/blob/main/.stagefreight.yml) |
| **Dogfood: everything at once** | StageFreight building itself | every target kind, `kind: command` docs build, `kind: pages`, self-hosted release channels | [StageFreight](https://github.com/PrPlanIT/StageFreight/blob/main/.stagefreight.yml) |

!!! note "More scenarios coming"
    A **GitLab CI/CD component** publisher (`kind: gitlab-component`) will be added once a
    public example repo opens up. Until then, [Configuration › Targets](config/publish.md)
    documents it.
