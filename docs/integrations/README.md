# StageFreight Integrations

One `.stagefreight.yml` drives every forge and registry via each platform's native API.
This page is the **per-provider** capability matrix; everything provider-agnostic — the
lifecycle, digest-preserving promotion, security scanning, badges, dependency updates — is in
[Features](../features.md).

## Forges

`stagefreight ci render <forge>` generates a native, audition-enforced pipeline; the forge
client handles releases/PRs/commits over the API. **Live validated** = the full pipeline
has run end-to-end on that platform; every render is implemented and golden-tested regardless.

| Forge | render | releases | PRs / MRs | catalog component | badges + README | live validated |
|-------------|:---:|:---:|:---:|:---:|:---:|:---:|
| GitLab | ✓ | ✓ | ✓ | ✓ publish + release link | ✓ | ✓ |
| GitHub | ✓ | ✓ | ✓ | — | ✓ | ✓ (hosted) |
| Gitea | ✓ | ✓ | ✓ | — | ✓ | ✗ |
| Forgejo | ✓ | ✓ | ✓ | — | ✓ | ✗ |
| Azure DevOps | ✓ | —¹ | ✓ | — | ✓ | ✗² |

Live runs so far: GitLab (StageFreight's own CI) and GitHub-hosted (a downstream project).
GitHub/Gitea/Forgejo share one Actions render backend, so that run covers the render for
all three; Gitea/Forgejo's live runtime (OIDC, runner, DinD) is unproven. Per-forge
checklists live in the integration folders.

¹ Azure DevOps has no native git-release object; release surfaces return
`ErrNotSupported` by design (use tags).
² Azure's forge client is also not yet validated against a real instance — see
[`azuredevops/`](azuredevops/).

**GitLab standout:** StageFreight can publish a GitLab **CI Catalog component**
and link it from the release — see [GitLab → CI/CD Catalog components](gitlab/README.md#cicd-catalog-components).

## Self-hosted runners

Each forge page carries its own runner/agent setup:

- [GitLab](gitlab/README.md#runner) — Compose (runner + BuildKit + DinD).
- [Azure DevOps](azuredevops/README.md) — Kubernetes agent (buildkitd + DinD).
- [GitHub / Gitea / Forgejo](actions/README.md) — GitHub-hosted works as-is; a self-hosted
  Actions runner guide is pending.

## Registries

Pushes are **digest-preserving** (the bytes review approved are the bytes
published — no rebuild). Retention is restic-style additive policies
(`keep_last`/`keep_daily`/…). On top of that, per provider:

| Registry | push + retention | repo README / description sync |
|--------------|:---:|---|
| Docker Hub | ✓ | **full README** sync |
| GHCR | ✓ | description sync |
| Quay | ✓ | short description |
| Harbor | ✓ | short description + OCI referrers |
| JFrog | ✓ | — |
| Gitea registry | ✓ | — |
| GitLab registry | ✓ | — |
| local (daemon) | ✓ (prune via `docker rmi`) | — |

**Docker Hub standout:** StageFreight syncs your repository's full README/overview
to Docker Hub from the repo, so the registry page and the source stay in step
without a manual copy-paste.
