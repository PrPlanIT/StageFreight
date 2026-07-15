# Design

A high-level tour of what StageFreight actually does when a pipeline runs, and the ideas
behind it. Read this to build a mental model; drop into [Configuration](../config/index.md)
when you want to *drive* a specific piece.

## The pipeline, phase by phase

A run moves through five load-bearing phases. The exact graph depends on `lifecycle.mode`,
but the canonical lifecycle is:

**audition → perform → review → publish → narrate**

- **Audition** — the pre-flight gate that runs before anything is built: lint (content,
  freshness, secret, and hygiene checks), and in GitOps mode the Kustomize/manifest
  validation.
- **Perform** — produce artifacts in containers: images, binaries, `kind: command` outputs (docs).
- **Review** — inspect and approve produced artifacts before anything is published.
- **Publish** — push images, cut releases, deploy pages, run retention.
- **Narrate** — compose repo-facing content (badges, includes) and commit it.

The authoritative phase sequence and how each phase gates the next live in
[Pipeline flow](pipeline-flow.md).

## Deep dives

The notes below explain the load-bearing mechanisms:

- [Pipeline flow](pipeline-flow.md) — the authoritative phase graph and how phases gate.
- [Invariants](invariants.md) · [Boundaries](boundaries.md) — the hard rules and package structure.
- [Perform build contributors](perform-build-contributors.md) — how artifacts are produced.
- [Content-store lifecycle](content-store-lifecycle.md) — carrying artifact bytes across phases.
- [CI render](ci-render.md) — how a forge-neutral pipeline becomes native CI YAML.
