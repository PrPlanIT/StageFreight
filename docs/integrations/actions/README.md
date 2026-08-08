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

Render, tags, and credentials are covered in [CI Integration](../ci.md).

## Runners

- **GitHub-hosted** runners work as-is — no setup, and the only live-validated path.
- **Self-hosted** Actions runners (GitHub, Gitea, Forgejo) need a Docker daemon — and
  BuildKit for image builds — reachable from the job; a packaged deployment isn't
  published yet. The GitLab [runner stacks](../gitlab/runner/docker/README.md) are a
  reference for the required companions.

## Credentials

GitHub uses the built-in `GITHUB_TOKEN` by default; set `GH_TOKEN` to a PAT for broader
scope. Gitea and Forgejo use their platform tokens. See
[CI Integration → Credentials](../ci.md#credentials).
