# GitHub, Gitea & Forgejo

> **Status:** GitHub live-validated on hosted runners; Gitea and Forgejo render-supported,
> live runtime (OIDC, runner, DinD) unproven.

These three forges share one golden-tested Actions render backend, so setup is identical
apart from the workflow path and the runner. StageFreight renders a native workflow and
drives releases/PRs/commits over each forge's API.

## Rendering

`stagefreight ci render <forge> --write` writes:

| Forge | Workflow file |
|---|---|
| `github` | `.github/workflows/stagefreight.yml` |
| `gitea` | `.gitea/workflows/stagefreight.yml` |
| `forgejo` | `.forgejo/workflows/stagefreight.yml` |

The pipeline model — render, tags, the five-job lifecycle — is in
[Getting Started](../../getting-started.md).

## Runners

- **GitHub-hosted** runners work as-is — no setup, and the only live-validated path.
- **Self-hosted** Actions runners (GitHub, Gitea, Forgejo) need a Docker daemon — and
  BuildKit for image builds — reachable from the job; a packaged deployment isn't
  published yet. The GitLab [runner stacks](../gitlab/README.md#runner) are a reference for
  the required companions.

### Build engine

The rendered workflow injects no dind service and no `DOCKER_HOST`: build-engine
provisioning is the runner's job. At runtime StageFreight detects the engine in order
`BUILDKIT_HOST` → `DOCKER_HOST` → local Docker socket, so a hosted runner uses its
auto-mounted socket and a self-hosted runner uses whatever buildkitd/dind the operator
exposes at the runner level. A workflow-level dind would shadow that operator engine, cannot
share TLS certs with a hosted job, leaks `DOCKER_HOST` into the runner's own host-side Docker
calls, and is not resolvable by name from a container job.

This is safe on ephemeral builders because the image never lives only in the daemon: the
build always exports an OCI layout under `.stagefreight/`, which travels perform → publish as
a job artifact. The cost on hosted runners is moving the full layout between jobs.

## Generated workflow

Details a hand-edit or a renderer change must preserve:

- **Phase handoff.** `upload-artifact` sets `include-hidden-files: true` (v4.4.0+ drops
  dot-paths such as `.stagefreight/` by default) and stores files relative to their common
  ancestor, so the artifact carries no `.stagefreight/` prefix. `download-artifact` therefore
  extracts with `path: .stagefreight`; extracting to `.` flattens the handoff into the
  workspace root and the next phase cannot find the pipeline state.
- **Workspace.** `SF_CI_WORKSPACE` is `$PWD`, not `${{ github.workspace }}` — in a container
  job that expression is the host path, which does not exist inside the container.
- **Cross-run cache.** Each phase job restores and saves `/stagefreight/cache` with
  `actions/cache`, keyed `stagefreight-cache-<os>-<phase>-<hashFiles(lockfiles)>` with a
  per-phase prefix restore key. The key is per phase because a GitHub cache key is
  write-once; a shared key races jobs. `hashFiles` ignores absent lockfiles, so one glob list
  covers every language. BuildKit's image-layer cache lives in the builder and is not
  covered.

## Credentials

Each forge's client uses its own access token; the repo auto-detects from `GITHUB_REPOSITORY`
(Actions) or `CI_REPO` (Woodpecker).

- **GitHub** — the built-in `GITHUB_TOKEN` works by default; set `GH_TOKEN` to a PAT for broader
  scope (e.g. pushing to another repo). The workflow requests `contents: write` on jobs that
  commit back.
- **Package registries** — each registry's credential prefix renders as
  `<PREFIX>_TOKEN: ${{ secrets.<PREFIX>_TOKEN || <forge default> }}` (likewise `_USER`), e.g.
  `secrets.GHCR_TOKEN || secrets.GITHUB_TOKEN`. The built-in token works with no setup; a
  repo secret of that name overrides it. A fork's `GITHUB_TOKEN` is often read-only by
  organization policy, and a `403` on package push means authenticated but unauthorized —
  add the `<PREFIX>_TOKEN` secret rather than changing config.
- **Multi-forge configs** — when one `.stagefreight.yml` runs on several forges (e.g. a
  GitLab origin and a GitHub mirror), scope targets whose registry is only reachable or
  credentialed on some forges with `when: { forges: [gitlab] }` instead of deleting them.
- **Gitea** — `GITEA_TOKEN`: a Gitea **access token** with repository read/write scope; drives
  commits, PRs, releases, and tags over the `/api/v1` REST API.
- **Forgejo** — `FORGEJO_TOKEN`: a Forgejo access token with the same repository read/write
  scope (the client shares Gitea's `/api/v1` backend).
