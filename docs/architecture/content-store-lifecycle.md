# StageFreight — Content Store Lifecycle

The content store (CAS, `src/cas`) is **not infrastructure**. It is a
content-addressed *staging scratchpad* that carries the exact bytes built in
`perform` through `review` and `publish`. It is closer to `/tmp` than to a
registry, and it must be treated that way or it becomes a cross-tenant footgun.

---

## The lifecycle

```
perform  → writes the OCI layout to the store (verify-on-write)
review   → reads + re-hashes the store (scan)        [read-only]
publish  → reads + re-hashes the store, distributes  [terminal reader]
publish  → RETIRES the store (same workspace)        [end of life]
```

The store has a defined **beginning** (perform) and now a defined **end**
(publish). It is a transient bounded by the perform→publish window — not durable
state.

---

## Invariant 1 — the store is workspace-scoped, never shared

> Every CAS path and every CAS lifecycle operation is bounded to a single job
> workspace. Never a runner-scoped, shared-volume, or global path.

The store lives at `<workspace>/.stagefreight/objects/`. GitLab (and every other
executor StageFreight targets) gives each job an isolated workspace/container, so
two pipelines — same project or different — on one runner have **separate**
stores. A cleanup operates on the current job's directory and **physically cannot
reach another pipeline's store.**

**Enforcement:** the path is derived in exactly one place,
`cas.WorkspaceObjectsDir(workspaceRoot)`, and the lifecycle constructor
`cas.NewWorkspaceStore(workspaceRoot)` takes the *workspace root*, not an
arbitrary path — so a caller cannot relocate the store onto a shared mount.
`workspace_test.go` fails if the store root ever escapes its workspace, or if
`Retire` ever reaches outside the calling workspace.

**The forbidden changes** (they would break this invariant and reintroduce the
footgun): moving the store onto the shared `/stagefreight` runner volume; mounting
a persistent cache into `.stagefreight/objects`; "reusing the CAS across jobs"
for speed; a single-container monorepo fanout sharing one store. The danger is
not today's behavior — it is future architecture drift, which is why this is an
enforced rule and not a comment.

---

## Invariant 2 — the store is *retired by publish*, not garbage-collected

> The CAS is closed by its terminal reader (publish), by deterministic ownership.
> It is never swept by a background or runner-wide GC.

This is the distinction that keeps it safe, and it separates two classes of
state StageFreight had been implicitly mixing:

| class | examples | cleanup |
|-------|----------|---------|
| **runner-scoped** (shared) | buildkit cache, container layers, registry retention, `/stagefreight` mounts | runner-wide GC, with fences |
| **workspace-scoped** (transient) | `.stagefreight/objects`, per-job artifacts, OCI export staging | per-job retirement, no cross-job awareness |

The CAS is the second kind. Plugging it into a runner-wide GC sweep — the same
mechanism that prunes buildkit cache — is exactly the bug that would delete a
concurrent project's store. "Same philosophy" (transient state is retired) does
**not** mean "same shared sweep."

---

## Invariant 3 — ingest is single-copy

> Writing the layout into the store must not duplicate it on disk.

The OCI layout is exported to a temp dir under the same workspace `.stagefreight/`
(same filesystem as the store), and `Put` **hardlinks** it in (`copyTree` →
`os.Link`, falling back to a byte copy only across filesystems). Without this, a
build held the full image ~2–3× simultaneously (buildx export + store copy +
artifact tar) — a transient spike that has crashed tight runners. Hardlinking
makes the layout occupy disk once.

---

## What bounds the store (defense in depth)

1. **Ingest is single-copy** (hardlink) — no transient spike.
2. **Publish retires it** — explicit end of life, same workspace.
3. **Workspace wipe** — the executor clears the workspace between jobs, the
   backstop if a pipeline never reaches publish.

The store is safe because all three are workspace-bounded. It is *not* safe to
rely on any one of them alone — and never on a global GC.
