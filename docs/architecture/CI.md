# CI Integration

StageFreight ships provider skeletons that translate CI context into a normalized contract. All behavior is configured in `.stagefreight.yml` — the skeleton only handles environment mapping and job structure.

## GitLab CI

### Skeleton

Copy `integrations/gitlab/.gitlab-ci.yml` into your project root. The skeleton defines five stages:

| Stage | Job | Runs on | Purpose |
|-------|-----|---------|---------|
| deps | `dependency-update` | default branch | Dependency resolution, advisory enrichment, auto-commit |
| build | `build-image` | default branch, tags | Container build, push, digest capture |
| security | `security-scan` | default branch, tags | Trivy + Grype vulnerability scan, SBOM, advisory bridge |
| docs | `generate-docs` | default branch, tags | Badge generation, narrator, reference docs |
| release | `create-release` | tags only | Forge release creation with notes, assets, registry links |

### Required CI/CD Variables

Set these in **Settings > CI/CD > Variables**:

| Variable | Scope | Required by | Notes |
|----------|-------|-------------|-------|
| `GITLAB_TOKEN` | Project or group access token | release, docs (push), deps (push) | Must have **`api`**, `read_repository`, `write_repository` scopes. Without `api`, release creation fails with 403 `insufficient_scope`. |
| `DOCKER_USER` | Registry username | build (push) | Or use `DOCKER_TOKEN` if your registry supports token-only auth. |
| `DOCKER_PASS` | Registry password/token | build (push) | Maps to `credentials: DOCKER` in `.stagefreight.yml`. |

**Token type guidance:**

- **Project access token** (recommended): Scoped to the project, rotatable, shows as a bot user in commit history. Create at **Settings > Access Tokens** with role **Maintainer** and scopes **`api`**, `read_repository`, `write_repository`.
- **Group access token**: Same scopes, shared across projects in a group.
- **`CI_JOB_TOKEN`** (automatic): GitLab provides this in every job. It can read project artifacts (used by the advisory bridge) and push to the project's container registry, but it **cannot create releases** — it lacks `api` scope. StageFreight uses `CI_JOB_TOKEN` as a fallback when `GITLAB_TOKEN` is not set.

### Token Resolution Order

StageFreight's GitLab forge client resolves tokens in this order:

1. `GITLAB_TOKEN` env var → uses `PRIVATE-TOKEN` header (full API access)
2. `CI_JOB_TOKEN` env var → uses `JOB-TOKEN` header (limited scope, no release creation)

If `GITLAB_TOKEN` is set, it is always preferred. Set it as a **masked, protected** CI/CD variable.

### Registry Credentials

Registry auth uses the `credentials` field in `.stagefreight.yml`:

```yaml
targets:
  - id: dockerhub
    kind: registry
    url: docker.io
    path: yourorg/yourapp
    credentials: DOCKER    # → DOCKER_TOKEN or DOCKER_USER + DOCKER_PASS
```

Resolution: `{PREFIX}_TOKEN` is tried first, then `{PREFIX}_USER` + `{PREFIX}_PASS`.

Set the corresponding variables in CI/CD settings. For Docker Hub, this is typically `DOCKER_USER` + `DOCKER_PASS` (where `DOCKER_PASS` is a Docker Hub access token, not your password).

### Cross-Pipeline Advisory Bridge

The `dependency-update` job fetches security advisories from the **previous pipeline's** `security-scan` job artifacts via the GitLab API. This requires:

- The `security-scan` job declares `artifacts: paths: [.stagefreight/security/]` (already in the skeleton)
- The token used by `dependency-update` can read project artifacts (`CI_JOB_TOKEN` is sufficient for this)

If no prior security artifacts exist (first pipeline, or security was disabled), deps runs normally without advisory enrichment.

## GitHub Actions

GitHub skeleton: planned but not yet shipped. The normalized `SF_CI_*` environment variable contract is the same — only the job structure differs.

## Gitea / Forgejo

Gitea skeleton: planned but not yet shipped. Works with both Woodpecker CI and Gitea Actions.
