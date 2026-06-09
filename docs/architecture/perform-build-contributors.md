# Perform Build Contributors — binary, image, crucible

> **Status: implemented.** Describes how the perform lifecycle actually builds things, so the
> three build strategies stay on one transport-correct path instead of drifting into copies.

## The spine

A perform run is **one domain-ordered narrative** — `Detect → Plan → Build → Verify → Publish`
(`build/domains/run.go`). The domains own execution order and rendering; **build strategies are
CONTRIBUTORS** that join the domains they care about (`build/domains/domain.go`). A contributor never
owns a spine — it supplies rows. Adding a strategy adds rows under Build/Publish, never a new
pipeline.

The runner type-asserts each registered contributor against the per-domain interfaces (`Detector`,
`Planner`, `Builder`, `Verifier`, `Publisher`) and calls only those it implements. `Applies(rc)`
gates participation.

## The three contributors

| contributor | `Applies` when | builds |
| --- | --- | --- |
| `binaryContributor` (`build/contributors/binary.go`) | a build is `kind: binary` | Go binaries → archives |
| `imageContributor` (`build/docker/image_contributor.go`) | a build is `kind: docker` **and `build_mode` is empty** | a normal app image |
| `crucibleContributor` (`build/docker/crucible_contributor.go`) | a build is `kind: docker` **and `build_mode: crucible`** | StageFreight's 2-pass self-proving self-build |

`Order()` renders them binary(10) → image(15) → crucible(20). Image and crucible are mutually
exclusive **per build** (`build_mode` empty vs `crucible`), so they never collide on the same entry.

### Why `imageContributor` exists

Before it, the perform spine had **only** binary + crucible. A plain `kind: docker` build (no
`build_mode`) had **no lifecycle contributor** — it built only via the standalone `stagefreight
docker build` command (the legacy `pipeline` path), so `ci run perform` silently built **nothing**
for a normal app that doesn't self-build. `imageContributor` closes that: it drives the same `image`
engine (`build/docker/image_engine.go`) through Detect/Plan/Build so the three valid shapes all build
in the lifecycle — **binary-only, image-only, binary+image**. Crucible stays StageFreight-specific.

## One transport-correct build core (do not re-duplicate)

`executePhase` (legacy), `crucibleContributor.Publish`, and `imageContributor.Build` **share** these —
they are not copied:

- `executeBuildPass` (`build/docker/crucible.go`) — buffers buildx output and renders a structured
  box; the raw log is shown only collapsed-on-failure. Use a **distinct section name** (the image
  contributor uses `"Image"`) because the domain runner renders its own `"Build"` box from the
  returned rows.
- `setupTransportPlan` (`build/docker/execute.go`) — stages digest-capture metadata + OCI-layout
  export on image steps, gated by `store.RequiresOCIExport()`. Sets only `MetadataFile`/`OCILayoutDir`,
  never `Push`/`Load` — the retain-vs-push decision stays with the caller.
- `applyImageBuildStrategy` (`build/docker/plan.go`) — the retain-vs-push decision: under transport,
  perform does **not** push (publish promotes the retained bytes); else load (single-platform) or push
  (multi-platform).
- `captureArtifactDigests` + `persistArtifactsRecords` (`build/docker/execute.go`) — digest from
  `containerimage.digest` only; retain the OCI layout to the content store. See
  [[content-store-lifecycle]] / [[transport-rollout]].
- `recordPublicationOutcomes` (`build/docker/record_outcomes.go`) — buildx publications →
  `rb.Record` push outcomes, **no attestation** (the crucible/image path). `executePhase` keeps its
  own `recordPushOutcome` + attestation path; the divergence is intentional, not unifiable.

The invariant: there is **one** path that builds → captures digest → retains → records, shared three
ways. Duplicating it is how the perform/publish retention guarantees rot.

## `resolveBranch` / dev-tag resolution (tagless projects)

Two related fixes so a new project's dev images tag correctly before its first version tag:

- `resolveBranch` (`build/docker/image_engine.go`) honours the CI-provided branch
  (`SF_CI_BRANCH` → `CI_COMMIT_BRANCH` → `GITHUB_REF_NAME`) **before** the version-info branch, which
  degrades to a synthetic `"unknown"` when git is absent in the build env. Otherwise `"unknown"` wins,
  `when: {branches: [main]}` targets match nothing, and **zero tags resolve**.
- `gitver.SyntheticVersion()` builds the no-detection fallback `VersionInfo` from the CI env
  (`SF_CI_SHA` / branch) instead of hardcoding `"unknown"`, so `dev-{sha:8}` resolves to the real
  commit. Used by both the image engine and the binary contributor; pairs with
  `versioning.no_lineage.mode: explicit` for a project with no tags yet.
