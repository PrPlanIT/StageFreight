# StageFreight Integrations

One `.stagefreight.yml` drives every forge and registry. StageFreight speaks each
platform's native API, so beyond "build and push" it does the platform-specific
things a maintainer would otherwise wire by hand. This is the capability matrix —
what's universal, and what's special per provider.

## Forges

`stagefreight ci render <forge>` generates a native, audition-enforced pipeline
from your config; the forge client handles releases/PRs/commits over the API.

The columns are observations, not a maturity label: what's implemented, and
whether it has actually run against a live instance. **Live validated** means the
full pipeline has executed on that platform — a fact, not a judgment about how
complete the code is (every render is implemented and golden-tested).

| Forge | render | releases | PRs / MRs | catalog component | badges + README | live validated |
|-------------|:---:|:---:|:---:|:---:|:---:|:---:|
| GitLab | ✓ | ✓ | ✓ | ✓ publish + release link | ✓ | ✓ |
| GitHub | ✓ | ✓ | ✓ | — | ✓ | ✗ |
| Gitea | ✓ | ✓ | ✓ | — | ✓ | ✗ |
| Forgejo | ✓ | ✓ | ✓ | — | ✓ | ✗ |
| Azure DevOps | ✓ | —¹ | ✓ | — | ✓ | ✗² |

GitLab is the only forge StageFreight has run end-to-end — it builds itself there.
GitHub/Gitea/Forgejo share one golden-tested Actions render backend, so validating
one largely validates the three; what's unproven is the live runtime (OIDC,
runner, DinD), not the code. A `✗` graduates to `✓` with a real run — the
integration folders carry the checklists.

¹ Azure DevOps has no native git-release object; release surfaces return
`ErrNotSupported` by design (use tags).
² Azure's forge client is also not yet validated against a real instance — see
[`azuredevops/`](azuredevops/).

**GitLab standout:** StageFreight can publish a GitLab **CI Catalog component**
and link it from the release. (Driving StageFreight *via* a component is
deprecated — render is the path — but the publish/discoverability capability
stays. See [`../docs/GitLab-Components.md`](../docs/GitLab-Components.md).)

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
