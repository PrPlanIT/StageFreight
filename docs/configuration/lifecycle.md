# Lifecycle Modes

The single most architecturally significant choice: `lifecycle.mode` selects the phase graph
the pipeline runs — build container images (`image`, the default), validate GitOps manifests
(`gitops`), reconcile governance (`governance`), or drive the docker lifecycle (`docker`).
Each mode has its own config section.

!!! example "Real examples"
    `dungeon` runs `mode: gitops`; `MaintenancePolicy` runs `mode: governance` — see
    [Quick Start](../quickstart/index.md).

--8<-- "docs/modules/config-reference.md:lifecycle"

--8<-- "docs/modules/config-reference.md:gitops"

--8<-- "docs/modules/config-reference.md:governance"

--8<-- "docs/modules/config-reference.md:docker"
