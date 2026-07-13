# How It Works

A high-level tour of what StageFreight actually does when a pipeline runs, and the ideas
behind it. Read this to build a mental model; drop into [Configuration](../config/index.md)
when you want to *drive* a specific piece.

## The pipeline, phase by phase

A run moves through a sequence of phases; the exact graph depends on `lifecycle.mode`. The
load-bearing phases in image mode are:

- **Lint** — content, freshness, secret, and hygiene gates.
- **Perform** — produce artifacts in containers: images, binaries, `kind: command` outputs (docs).
- **Review** — inspect/approve produced artifacts before anything is published.
- **Narrate** — compose repo-facing content (badges, includes) and commit it.
- **Publish** — push images, cut releases, deploy pages, run retention.

There are further phases (e.g. `audition`) and mode-specific graphs. The authoritative phase
sequence lives in [Pipeline flow](../architecture/pipeline-flow.md); the list above covers
the phases whose behavior is documented rather than asserting an exact graph.

## Deep dives

The notes below explain the load-bearing mechanisms:

- [Pipeline flow](pipeline-flow.md) — the authoritative phase graph and how phases gate.
- [Invariants](invariants.md) · [Boundaries](boundaries.md) — the hard rules and package structure.
- [Perform build contributors](perform-build-contributors.md) — how artifacts are produced.
- [Content-store lifecycle](content-store-lifecycle.md) — carrying artifact bytes across phases.
- [CI render](ci-render.md) — how a forge-neutral pipeline becomes native CI YAML.
