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

## Credentials

Each forge's client uses its own access token; the repo auto-detects from `GITHUB_REPOSITORY`
(Actions) or `CI_REPO` (Woodpecker).

- **GitHub** — the built-in `GITHUB_TOKEN` works by default; set `GH_TOKEN` to a PAT for broader
  scope (e.g. pushing to another repo). The workflow requests `contents: write` on jobs that
  commit back.
- **Gitea** — `GITEA_TOKEN`: a Gitea **access token** with repository read/write scope; drives
  commits, PRs, releases, and tags over the `/api/v1` REST API.
- **Forgejo** — `FORGEJO_TOKEN`: a Forgejo access token with the same repository read/write
  scope (the client shares Gitea's `/api/v1` backend).
