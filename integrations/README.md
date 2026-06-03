# StageFreight Integrations

One `.stagefreight.yml` drives every forge and registry. StageFreight speaks each
platform's native API, so beyond "build and push" it does the platform-specific
things a maintainer would otherwise wire by hand. This is the capability matrix —
what's universal, and what's special per provider.

## Forges

`stagefreight ci render <forge>` generates a native, audition-enforced pipeline
from your config; the forge client handles releases/PRs/commits over the API.

| Forge | CI render | releases | PRs / MRs | catalog component | badges + README inject |
|-------------|:---:|:---:|:---:|:---:|:---:|
| GitLab | ✓ | ✓ | ✓ | ✓ publish + release link | ✓ |
| GitHub | ✓ | ✓ | ✓ | — | ✓ |
| Gitea | ✓ | ✓ | ✓ | — | ✓ |
| Forgejo | ✓ | ✓ | ✓ | — | ✓ |
| Azure DevOps | ✓ | —¹ | ✓ | — | ✓ |

¹ Azure DevOps has no native git-release object; release surfaces return
`ErrNotSupported` by design (use tags). Azure client is **experimental** until
live-validated — see [`azuredevops/`](azuredevops/). Render is native and stable.

**GitLab standout:** StageFreight can publish a GitLab **CI Catalog component**
and link it from the release. (Driving StageFreight *via* a component is
deprecated — render is the path — but the publish/discoverability capability
stays. See [`../templates/`](../templates/).)

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

## Universal (every provider, from one config)

- Lifecycle: `audition → perform → review → publish → narrate`, rendered natively
  per forge.
- Digest-preserving promotion (perform retains to a content store; publish
  distributes the exact reviewed digest).
- Security scanning (review) and release notes with a configurable security
  summary.
- Badge generation and README/shield injection.
- Dependency auto-update and the narrator changelog/inventory.

## Self-hosted runner deployments

- GitLab: [`gitlab/docker/`](gitlab/docker/) — Compose (runner + buildkitd + DinD).
- Azure DevOps: [`azuredevops/k8s/`](azuredevops/k8s/) — Kubernetes (agent +
  buildkitd + DinD), same trust split.
