# StageFreight — Feature Matrix

A capability-by-capability comparison of **what StageFreight gives you declaratively** versus what
the same outcome costs on raw CI platforms, with the closest dedicated tool and an honest "how hard
is this to build yourself" rating.

The point of this document: most of these capabilities are things you *can* do anywhere — but on a
raw CI platform you assemble them from actions, plugins, and hand-written API calls against each
backend. StageFreight collapses that into one `.stagefreight.yml` and talks to the backends for you.
Where StageFreight is **behind** or **not yet GA**, this document says so plainly.

**One config, many CI hosts.** `stagefreight ci render <forge>` emits a *native* pipeline for
**GitLab, GitHub Actions, Gitea, Forgejo, and Azure DevOps** from the same `.stagefreight.yml`. Your
pipeline isn't married to a vendor — switching CI hosts is a *re-render, not a rewrite*. Nothing else
in this matrix offers that, because every other tool *is* one of those vendors.

> This is a **living document.** Capability coverage is complete; per-feature commit provenance
> (the "Since" lines) is seeded and being filled in over time. See [Maintaining this doc](#maintaining-this-doc).

## How to read it

Columns are the tools; rows are capabilities. StageFreight is first.

| Mark | Meaning |
|---|---|
| ✅ | First-class & **declarative** — you configure it, the tool does it |
| ➕ | Available, but via a **marketplace action / plugin / extra service** you wire up |
| 🔧 | Possible only by **writing it yourself** (scripts + direct backend API calls) |
| 🚧 | **Designed but gated / experimental** in StageFreight — not yet GA |
| — | Not meaningfully a thing on that platform |

**DIY effort** = how much work it is to pull off yourself / talk to the backend directly:
**Low** (a few lines) · **Med** (a real script + auth) · **High** (a subsystem of its own).

The **Specialized tool** column names the best-in-class single-purpose tool, so you can see what
StageFreight is consolidating — and where that dedicated tool is still the stronger standalone choice.

---

## 1. Build

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Multi-platform container build | ✅ | ➕ buildx action | 🔧 buildx by hand | ➕ buildx / depot | Med |
| Go binary build (multi-OS/arch) | ✅ | ➕ setup-go + matrix | 🔧 matrix by hand | ➕ goreleaser | Med |
| **Reproducible build self-proof** (crucible) | ✅ | 🔧 | 🔧 | — (niche: rebuilderd) | **High** |
| Build cache (local + registry-backed) | ✅ | ➕ cache action | ➕ cache: | ➕ buildx cache | Med |
| Build ordering / dependency graph | ✅ | ➕ needs: | ✅ needs: | — | Low |
| UPX binary compression | ✅ | 🔧 | 🔧 | ➕ goreleaser | Low |
| Docker-compose drift detection | ✅ | 🔧 | 🔧 | ➕ ansible/terraform | High |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** Docker **buildx/BuildKit** (`docker buildx build`, OCI-layout export, digest capture), Go SDK (governed download — see §11), buildkitd or DinD host.
- **Crucible** = two passes: pass-1 gestation (`--output type=oci`, never pushed), pass-2 re-runs the candidate natively and **diffs the bytes** to prove the image is reproducible. Almost no CI platform does this for you — by hand you'd build twice, extract artifacts, and compare digests across a coherent backend.
- **Otherwise:** hand-write `docker buildx` invocations, parse `--metadata-file`/digest output, manage `--cache-from`/`--cache-to`, and (for binaries) a build matrix per OS/arch with ldflags version injection.
- **Tutorials:** [docker/build-push-action](https://github.com/docker/build-push-action) · [buildx cache](https://docs.docker.com/build/cache/backends/) · [goreleaser builds](https://goreleaser.com/customization/builds/) · [reproducible-builds.org](https://reproducible-builds.org/)
- **Since:** binary build engine + caching shipped through v0.6.1 (Go caches on the `/stagefreight` mount, commit `12b8806`; cache-state row `67d916f`). Crucible: ≤ v0.6.0 *(TODO: pin commit)*.
</details>

---

## 2. Publish to Registries

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Push to one registry | ✅ | ✅ login+push | ✅ | ✅ docker | Low |
| Push to **many registries** (8 providers) | ✅ | 🔧 per-registry | 🔧 per-registry | ➕ crane/regctl | Med |
| Tag templating (`{version}`, `{major}.{minor}`, `{sha:8}`) | ✅ | 🔧 | 🔧 | ➕ docker/metadata-action | Med |
| Sync README to registry (Docker Hub, Harbor, Quay…) | ✅ | ➕ dockerhub-description | 🔧 | — | Med |
| Publish GitLab CI component to Catalog | ✅ | — | ➕ manual | — | Med |
| Native registry scan trigger (Harbor Trivy) | ✅ | 🔧 | 🔧 | — | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** Docker Hub (v2 + token-auth), GHCR (packages API), GitLab registry, **Harbor** (v2.0 — push, scan trigger, project ensure, description), JFrog Artifactory/JCR, Quay (v1), Gitea/Forgejo packages, local Docker daemon, generic OCI Distribution.
- **Otherwise:** for each registry, hand-write the token-auth dance, push, and (for description sync) the provider-specific description API — Docker Hub caps short desc at 100 chars / full at 25 KB; Harbor/Quay each differ.
- **Tutorials:** [GHA: push to multiple registries](https://docs.docker.com/build/ci/github-actions/multi-registry/) · [docker/metadata-action](https://github.com/docker/metadata-action) · [peter-evans/dockerhub-description](https://github.com/peter-evans/dockerhub-description) · [GitLab Catalog](https://docs.gitlab.com/ci/components/)
- **Since:** ≤ v0.6.0 *(TODO: pin commits per provider)*.
</details>

---

## 3. Retention & Cleanup

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| **Restic-style tag retention** (keep_last/daily/weekly/monthly/yearly) | ✅ | 🔧 | ➕ cleanup policy (GitLab-only) | — | **High** |
| Retention across **any** registry | ✅ | 🔧 | — | ➕ regctl scripts | High |
| Protect tag patterns from deletion | ✅ | 🔧 | ➕ regex keep | — | Med |
| Local daemon image pruning (`--load` dev builds) | ✅ | 🔧 | 🔧 | 🔧 docker rmi | Low |
| BuildKit cache prune + retention | ✅ | ➕ gha cache evicts | 🔧 | 🔧 | Med |
| Host hygiene (dangling images, exited containers, networks) | ✅ | — | — | 🔧 docker system prune | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backend:** each registry's delete API + shared-digest protection (won't delete a tag whose digest another kept tag still points at).
- **Otherwise:** GitLab has a built-in tag cleanup policy, but it's GitLab-only; everywhere else you hand-write the "list tags → apply keep policy → DELETE the losers" engine per registry, plus the digest-sharing safety. This is a genuine subsystem to build well.
- **Tutorials:** [GitLab cleanup policy](https://docs.gitlab.com/user/packages/container_registry/reduce_container_registry_storage/) · [restic forget policy](https://restic.readthedocs.io/en/stable/060_forget.html) (the model SF borrows) · [regctl](https://github.com/regclient/regclient)
- **Since:** ≤ v0.6.0 *(TODO)*.
</details>

---

## 4. Versioning & Tagging

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Version derived from git (no version file) | ✅ | ➕ git-describe action | 🔧 | ➕ semantic-release | Med |
| Tag-lineage sources (stable vs prerelease channels) | ✅ | 🔧 | 🔧 | ➕ semantic-release | High |
| Per-branch version formats (e.g. `dev-{sha}`) | ✅ | 🔧 | 🔧 | — | Med |
| Policy-enforced tag planner + approval | ✅ | 🔧 | 🔧 | ➕ semantic-release | Med |
| Generated tag annotation / highlights | ✅ | ➕ changelog action | ➕ | ✅ git-cliff | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backend:** git (tags, lineage), the glossary/change-language engine (§12) for highlights.
- **Otherwise:** semantic-release/git-cliff cover changelog + version bump well, but channel lineage ("this prerelease descends from which stable line") and per-branch version formats are usually bespoke bash around `git describe`.
- **Tutorials:** [semantic-release](https://semantic-release.gitbook.io/) · [git-cliff](https://git-cliff.org/) · [GHA git describe](https://github.com/marketplace/actions/git-describe)
- **Since:** `stagefreight tag` planner ≤ v0.6.0; v0.6.1 cut with it *(TODO: pin commit)*.
</details>

---

## 5. Releases & Artifacts

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Create a forge release | ✅ | ➕ gh-release action | ✅ release-cli | ✅ goreleaser | Low |
| Binary archives (tar.gz/zip) + **SHA256SUMS** | ✅ | 🔧 | 🔧 | ✅ goreleaser | Med |
| Rolling git-tag aliases (`v1`, `v1.2`) | ✅ | 🔧 | 🔧 | ➕ | Med |
| **Cross-forge** release sync (GitLab→GitHub mirror) | ✅ | 🔧 | 🔧 | — | High |
| Security summary embedded in release notes | ✅ | 🔧 | 🔧 | — | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** GitHub / GitLab / Gitea / Forgejo release APIs (asset upload, release links, rolling tags). **Azure DevOps: releases honestly return `ErrNotSupported`** (no native git-release object) — see [honest status](#honest-status).
- **Otherwise:** goreleaser is the strongest standalone here (archives, checksums, release, on GitHub/GitLab). What it doesn't do is mirror a release to a *second* forge with its own identity, or thread your scan/advisory summary into the notes — those stay bespoke.
- **Tutorials:** [goreleaser](https://goreleaser.com/) · [softprops/action-gh-release](https://github.com/softprops/action-gh-release) · [GitLab release-cli](https://docs.gitlab.com/ci/yaml/#release)
- **Since:** binary archives + SHA256SUMS landed in v0.6.1 *(TODO: pin commit)*; release core ≤ v0.6.0.
</details>

---

## 6. Lint / Code Quality

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Delta-only (changed-files) linting | ✅ | ➕ paths-filter | ➕ rules:changes | ➕ pre-commit | Med |
| Cache-aware lint with TTL eviction | ✅ | ➕ cache action | ➕ cache: | ➕ pre-commit | Med |
| Built-in modules (secrets, tabs, line endings, freshness…) | ✅ | ➕ many actions | ➕ many | ✅ pre-commit/megalinter | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backend:** local filesystem + git (diff vs target branch); embeds gitleaks-style secret scanning.
- **Otherwise:** pre-commit / MegaLinter are the standalone analogs and have a larger rule ecosystem; StageFreight's value is that lint is one phase of the same lifecycle with shared caching, not a separate tool to wire.
- **Tutorials:** [pre-commit](https://pre-commit.com/) · [MegaLinter](https://megalinter.io/) · [GHA paths-filter](https://github.com/dorny/paths-filter)
- **Since:** ≤ v0.6.0 *(TODO)*.
</details>

---

## 7. Security & Supply Chain

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Image vuln scan (Trivy + Grype) | ✅ | ➕ trivy-action | ➕ template | ✅ trivy/grype | Med |
| SBOM (SPDX + CycloneDX) | ✅ | ➕ sbom-action | ➕ | ✅ syft | Med |
| Scan the **exact built bytes** (OCI layout, no registry round-trip) | ✅ | 🔧 | 🔧 | 🔧 | High |
| Cross-pipeline advisory bridge | ✅ | 🔧 | 🔧 | — | High |
| Image / artifact **signing** | 🚧 | ➕ cosign-installer | ➕ | ✅ cosign | Med |
| Provenance / OCI labels | ✅ | ➕ provenance attest | ➕ | ✅ cosign attest | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** Trivy, Grype, Syft, osv-scanner, govulncheck, cosign — all **governed/verified downloads** (§11). Scans can read the content-store **OCI layout directly** (`trivy --input` / `grype oci-dir:`), so you scan the bytes you built and review, not a re-pulled copy.
- **Signing is the honest gap:** today it's hardcoded `cosign sign --key … --tlog-upload=false`; method selection (keyless/OIDC-Fulcio/Rekor, KMS, hardware/YubiKey) and signed `SHA256SUMS` bundles are **designed but gated** (`docs/architecture/signing-trust-model.md`). If signing flexibility is your priority *today*, standalone cosign in your CI is ahead.
- **Tutorials:** [aquasecurity/trivy-action](https://github.com/aquasecurity/trivy-action) · [anchore/sbom-action](https://github.com/anchore/sbom-action) · [sigstore/cosign](https://docs.sigstore.dev/cosign/signing/signing_with_containers/) · [SLSA provenance](https://slsa.dev/)
- **Since:** scan/SBOM/advisory ≤ v0.6.0; signing **gated** (not shipped).
</details>

---

## 8. Dependency Updates

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Go module updates + verify | ✅ | ➕ dependabot | ➕ renovate | ✅ renovate | Med |
| Dockerfile base-image `FROM` updates | ✅ | ➕ dependabot | ➕ renovate | ✅ renovate | Med |
| Post-update vuln check (govulncheck) | ✅ | 🔧 | 🔧 | ➕ | Med |
| Direct-commit **or** MR promotion mode | ✅ | ➕ (PR only) | ➕ (MR only) | ➕ renovate | Med |
| Toolchain metadata recorded for SBOM | ✅ | — | — | — | High |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** Go toolchain (`go get`/`go mod tidy` via governed Go SDK), Dockerfile parsing, GitHub releases (for `FROM` tag bumps), the forge (for MR mode).
- **Honest scope:** **Renovate** covers far more ecosystems (npm, pip, gradle, helm, …) and is the stronger standalone updater. StageFreight's edge is that updates are part of *its* lifecycle (verify → vuln-check → its commit/MR conventions → its release notes), Go- and Dockerfile-focused for now.
- **Tutorials:** [Renovate](https://docs.renovatebot.com/) · [Dependabot](https://docs.github.com/code-security/dependabot)
- **Since:** direct/MR promotion present; this session confirmed `promotion: direct` is the StageFreight-repo default. ≤ v0.6.0 *(TODO)*.
</details>

---

## 9. Docs, Badges & READMEs

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Own SVG badges (no shields.io dependency) | ✅ | 🔧 | 🔧 | ➕ shields.io | Med |
| Marker-section injection into README/any file | ✅ | ➕ readme actions | 🔧 | ➕ markdown-magic | Med |
| Sync README to Docker Hub / registries | ✅ | ➕ dockerhub-description | 🔧 | — | Med |
| Generate CLI/config reference docs from code | ✅ | 🔧 | 🔧 | ➕ cobra docs | Med |
| Render build manifest contents into docs (`build-contents`) | ✅ | 🔧 | 🔧 | — | High |
| Auto-commit generated docs (with skip-ci classification) | ✅ | ➕ git-auto-commit | 🔧 | — | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** local files + git (auto-commit), registry description APIs, shields.io (only for the external `kind: props` badges; native `kind: badge` SVGs are self-owned).
- **Otherwise:** stitch together shields.io URLs, a README-injection action, a Docker Hub description action, and cobra-doc generation — each separate. The **`build-contents`** renderer (image inventory → README table) and the generated-commit **skip-ci classification** (a docs commit is synchronization output, not source intent — so it shouldn't re-trigger the lifecycle) are SF-specific.
- **Tutorials:** [shields.io](https://shields.io/) · [peter-evans/dockerhub-description](https://github.com/peter-evans/dockerhub-description) · [cobra doc gen](https://github.com/spf13/cobra/blob/main/site/content/docs/generating_documentation.md)
- **Since:** narrator `build:` ownership selector + docs `skip_ci` classification both v0.6.1 (`7d0feb1`, `bfca092`); narrator/badges core ≤ v0.6.0.
</details>

---

## 10. Transport & Trust (the differentiator)

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Content-addressed store carrying bytes across phases | ✅ | 🔧 artifacts | 🔧 artifacts | — | **High** |
| **Reviewed bytes == published bytes** guarantee | ✅ | 🔧 | 🔧 | — | High |
| Verify-on-write (digest re-hash between phases) | ✅ | 🔧 | 🔧 | — | High |
| Perform / Review / Publish phase split | ✅ | 🔧 stage gates | 🔧 stage gates | — | High |
| Build/publish manifest (`outputs.json`/`published.json`) | ✅ | 🔧 | 🔧 | — | Med |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backend:** a CAS (content-addressed store, FSStore today) carrying an OCI layout perform→review→publish; publish is the *sole* distributor.
- **Why it matters:** on raw CI, "build in one job, scan in another, push in a third" usually means each job **re-pulls or re-builds**, so what you scanned isn't provably what you shipped. SF's transport keeps the exact reviewed bytes and re-verifies the digest before distribution. This is the architectural heart of the project and has no off-the-shelf equivalent.
- **Tutorials:** conceptually adjacent: [SLSA build provenance](https://slsa.dev/spec/v1.0/provenance) · [in-toto attestations](https://in-toto.io/).
- **Since:** the perform/review/publish + manifest split is the v0.5→v0.6 line of work (domain-spine refactor + manifest `outputs.json`/`published.json` split, shipped through v0.6.x) *(TODO: pin commits)*.
</details>

---

## 11. Governed Toolchains

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| Resolve + **checksum-verify** + cache build tools | ✅ | ➕ setup-* (per tool) | 🔧 | ➕ asdf/mise | Med |
| One substrate for Go, Trivy, Syft, Grype, cosign, flux, kubectl, osv | ✅ | 🔧 (8 separate actions) | 🔧 | ➕ mise | High |
| Pin versions in config, hard-fail if unresolvable | ✅ | ➕ | 🔧 | ➕ mise | Med |
| No host-PATH fallback, no DinD requirement | ✅ | — | — | — | High |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backend:** official download URLs + published checksums for each tool; immutable cache with `.metadata.json` provenance, file-locked installs, persistent on the `/stagefreight` mount.
- **Otherwise:** a `setup-go` + a trivy-installer + a cosign-installer + … per tool, none of which give you a single governed, checksum-verified, provenance-recorded cache. mise/asdf are the closest standalone idea but don't verify against official checksums the same way.
- **Tutorials:** [mise](https://mise.jdx.dev/) · [actions/setup-go](https://github.com/actions/setup-go)
- **Since:** ≤ v0.6.0; persistent Go-cache path on the mount confirmed v0.6.1 (`12b8806`).
</details>

---

## 12. Orchestration, Conventions & Infra Modes

| Capability | StageFreight | GitHub Actions | GitLab CI | Specialized tool | DIY effort |
|---|---|---|---|---|---|
| **CI-vendor portability** — one config → GitLab · GitHub · Gitea · Forgejo · Azure 🚧 | ✅ | — | — | — | **High** |
| **Render the CI file itself** from one config | ✅ | — | — | — | **High** |
| Conventional-commit planner + change language (glossary) | ✅ | ➕ commitlint | ➕ | ✅ commitizen | Med |
| Generated-commit skip-ci policy (no self-triggering loops) | ✅ | 🔧 | 🔧 | — | Med |
| GitOps reconcile (Flux / [Argo 🚧]) + change-impact | ✅ | 🔧 flux scripts | 🔧 | ✅ flux/argo | Med |
| K8s endpoint exposure classification | ✅ | — | — | — | High |
| Control-repo / multi-repo governance mode | ✅ | 🔧 | 🔧 | — | High |

<details><summary>Backends · how you'd otherwise do it · tutorials · since</summary>

- **Backends:** the forge as a **render target** — five wired emitters (`gitlab`, `github`, `gitea`, `forgejo`, `azuredevops`), each forge-native with golden tests; git (commits), Flux/Argo + Kubernetes (gitops), Docker (compose drift).
- **Portability / no lock-in:** `render.Emit(forge, …)` dispatches to all five from one forge-neutral pipeline model, so the *same* `.stagefreight.yml` renders `.gitlab-ci.yml`, GitHub Actions workflows, Gitea/Forgejo, or Azure pipelines. To migrate CI hosts by hand you'd rewrite every pipeline in the new vendor's YAML dialect; here it's `stagefreight ci render <forge> --write`. (Azure DevOps is experimental; and the CLI `--help` still reads "Supported forges: gitlab" — stale, the code supports all five.)
- **`ci render` is unusual:** instead of you maintaining `.gitlab-ci.yml`/workflows by hand, SF generates them from `.stagefreight.yml`. The thing other tools assume you write, SF treats as output. (Caveat from real use: because it's generated + dogfooded, a CI-skeleton or config-schema change can require regeneration — see KnownIssues.)
- **Tutorials:** [commitlint](https://commitlint.js.org/) · [Flux](https://fluxcd.io/) · [Argo CD](https://argo-cd.readthedocs.io/)
- **Since:** generated-commit skip-ci classification v0.6.1 (`bfca092`/`124f3ba`); glossary + ci-render ≤ v0.6.0.
</details>

---

## Appendix A — Backends StageFreight speaks for you

Every row below is a system you'd otherwise authenticate to and call by hand.

| Category | Backends | What SF does against them |
|---|---|---|
| **Container registries** | Docker Hub, GHCR, GitLab, Harbor, JFrog, Quay, Gitea/Forgejo, local daemon, generic OCI | push, tag list, retention/delete, README sync, native scan trigger (Harbor), referrer discovery, digest verify |
| **Git forges** | GitHub/GHES, GitLab, Gitea, Forgejo, Azure DevOps 🚧 | releases, asset upload, multi-file commits, tags, MRs/PRs, pipeline cancel, artifact download |
| **Toolchains** | Go SDK, Trivy, Syft, Grype, osv-scanner, cosign, flux2, kubectl | resolve → checksum-verify → cache → invoke by absolute path |
| **Build** | Docker buildx / BuildKit, buildkitd, DinD, CAS content store | multi-arch build, OCI export, cache, crucible 2-pass, byte transport |
| **Security** | cosign 🚧, Trivy, Grype, Syft, osv-scanner, govulncheck | sign(gated)/attest, scan, SBOM, advisory bridge, provenance |
| **Infra** | Kubernetes, Flux / [Argo 🚧], Docker Compose, Ansible inventory | gitops reconcile/impact, compose drift, exposure classification |

---

## Honest status

Things this matrix marks 🚧 or that deserve a caveat, stated plainly:

- **Signing is gated.** Today it's `cosign sign --key … --tlog-upload=false` only. Keyless/OIDC/KMS/hardware and signed `SHA256SUMS` bundles are designed but not shipped. For signing flexibility now, standalone cosign is ahead.
- **Azure DevOps is experimental** and honestly returns `ErrNotSupported` for releases (no native git-release object).
- **OIDC** is a reserved seam, not implemented.
- **Multi-arch crucible** is deferred (arm64 ships via binaries; the image is single-arch by design for now).
- **Dependency updates** are Go + Dockerfile focused; Renovate covers more ecosystems.
- **Pre-1.0:** the config schema isn't frozen — expect breaking changes across versions, and regenerate the CI skeleton after upgrades.
- **`manifest diff`** is declared but not yet implemented.

Where the matrix shows StageFreight's real, hard-to-replicate value: **crucible reproducibility**, **multi-registry retention**, **the perform/review/publish byte-transport trust model**, **governed toolchains**, and **rendering the CI file itself** — these are the High-DIY-effort rows you'd otherwise build and maintain yourself.

---

## Maintaining this doc

- **Coverage** (rows) is complete as of v0.6.1. When a feature lands, add its row.
- **Provenance** (the "Since" lines) is being filled in. To pin a feature's introducing commit:
  `git log --oneline --reverse -- <path/to/feature> | head -1`, or map to the release tag it first shipped in (`git tag --contains <commit>`).
- Keep the comparison columns **fair** — the alternatives are capable; the story is *declarative + integrated*, not *only-SF-can*.
- Mark anything not-yet-GA as 🚧 and add it to [Honest status](#honest-status). Overstating readiness burns trust the first time an adopter hits the gap.
