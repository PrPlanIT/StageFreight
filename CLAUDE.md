- Repository Information:
  - Git repository: https://gitlab.prplanit.com/PrPlanIT/StageFreight
  - **Git Commits**: Only sign commits as sofmeright@gmail.com / SoFMeRight. No anthropic attribution comments in commits.

- CRITICAL RULES:
  - STAY ON TASK when following directions. NO BAND AID, NO WORK AROUNDS. If you think we need to give up or regroup, ASK. Don't make the call on your own to find alternative solutions or shortcuts.
  - I want things done exactly how I ask. If you want to offer an alternative, conversation should stop till I tell you if I agree/disagree.
  - **NEVER use `git commit`** — always use `stagefreight commit`. The binary is at `/usr/local/bin/stagefreight`.
  - **NEVER use `git tag`** — always use `stagefreight tag`. Supports `--minor`, `--patch`, `--major`, `--push`, `-y`. Enforces version policy and generates release highlights.

- Building & Testing:
  - **NO LOCAL GO TOOLCHAIN** — StageFreight dogfoods itself, all operations run in containers. Never run `go build`, `go mod tidy`, `go vet`, etc. locally.
  - **Always `docker pull` first** to get the latest image — stale local images cause confusing errors.
  - **Container invocation pattern** (use for ALL stagefreight commands):
    ```bash
    docker pull docker.io/prplanit/stagefreight:latest-dev
    docker run --rm -v "$PWD":/src -w /src \
      docker.io/prplanit/stagefreight:latest-dev \
      sh -c 'git config --global --add safe.directory /src && stagefreight <command>'
    ```
    - `safe.directory` is required because the container runs as a different user than the host mount owner — git refuses to operate without it.
    - Add `-v /var/run/docker.sock:/var/run/docker.sock` for commands that need Docker (e.g., `docker build`).
  - `--dry-run` to verify plan resolution without building. `--local` to load into daemon without pushing.
  - **CI pipeline** (`.gitlab-ci.yml`): Same dogfood approach — CI image is the latest dev. `stagefreight docker build` handles detect → lint → plan → build → push → retention.

- Architecture:
  - Go CLI at `src/cli/main.go`, commands under `src/cli/cmd/`
  - Build engine: `src/build/` (plan, buildx, tags, version detection)
  - Registry providers: `src/registry/` (dockerhub, ghcr, gitlab, quay, jfrog, harbor, gitea, local)
  - Forge abstraction: `src/forge/` (GitLab, GitHub, Gitea/Forgejo)
  - Lint engine: `src/lint/` with modules under `src/lint/modules/`
  - Config: `src/config/` — parsed from `.stagefreight.yml`

- Build Strategy:
  - **Single-platform**: `--load` into daemon → `docker push` each remote tag. Image exists locally AND remotely. Both local and remote retention work.
  - **Multi-platform**: `--push` directly (buildx limitation). No local copy. Remote retention only.
  - `provider: local` registries: images stay in the Docker daemon only. Retention prunes via `docker rmi`.

- Retention:
  - Restic-style additive policies: `keep_last`, `keep_daily`, `keep_weekly`, `keep_monthly`, `keep_yearly`
  - A tag survives if ANY policy wants to keep it
  - Config accepts integer shorthand (`retention: 10` = keep_last: 10) or full policy map
  - Tag patterns (from `tags:` field) are converted to regex for matching which remote tags are retention candidates

- Dependency Update (`stagefreight dependency update`):
  - Resolves outdated deps, applies updates, verifies, generates artifacts
  - Go modules: `go get` + `go mod tidy` via multi-strategy toolchain resolver (native → toolcache → container runtime)
  - Dockerfile: FROM image tag replacement with hash-guarded line edits
  - **Go directive sync**: When a golang builder image is bumped, the `go` directive in the owning `go.mod` is synced automatically via `go mod edit -go=<version>`. Module-aware — maps each Dockerfile to its nearest `go.mod`. Conflicting builder versions within a module are skipped with detailed reporting.
  - **Toolchain metadata**: Resolved build toolchains are recorded as first-class `ToolchainDependency` entries on `UpdateResult` for SBOM enrichment, release security output, and catalog provenance.
  - **Design philosophy**: StageFreight is the sole steward of the build/update process. It standardizes dependency management end-to-end so projects don't need ad-hoc scripts. Features are built generically — useful universally, never hardcoded to one project.

- Input Validation:
  - Registry URLs, image paths, tags, credentials, provider names, and regex patterns are all validated at plan time
  - Resolved tags are validated against OCI spec before any push
  - Fail fast with clear errors, not cryptic Docker failures
