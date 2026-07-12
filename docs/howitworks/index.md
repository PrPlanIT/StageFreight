# How It Works

A high-level tour of what StageFreight actually does when a pipeline runs, and the ideas
behind it. Read this to build a mental model; drop into [Configuration](../configuration/index.md)
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
sequence lives in [Pipeline flow](../architecture/pipeline-flow.md) — I've kept this list to
the phases whose behavior is documented, rather than assert an exact graph.

## Deep dives

The architecture notes below explain the load-bearing mechanisms:

- [Pipeline flow](../architecture/pipeline-flow.md) · [Boundaries](../architecture/boundaries.md) · [Invariants](../architecture/invariants.md)
- [Perform build contributors](../architecture/perform-build-contributors.md) · [Multi-arch strategy](../architecture/multi-arch-strategy.md) · [Transport rollout](../architecture/transport-rollout.md)
- [Release channels](../architecture/release-channels.md) · [Signing trust model](../architecture/signing-trust-model.md)
- [GitOps validation authority](../architecture/gitops-validation-authority-model.md) · [Persistence & identity](../architecture/persistence-identity.md)

!!! note "Work in progress"
    This tab will grow into a curated set of high-level breakdowns; the links above are the
    existing engineering deep-dives.
