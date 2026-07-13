# Targets

`targets:` declares **what StageFreight does with a build** — push image tags, sync a
registry README, publish a GitLab component, cut a forge release, publish an archive or
package, or deploy a static site. Each entry is a **discriminated union keyed by `kind`**:
the `kind` decides which other keys are valid.

Every target shares an **`id`** (unique) and a **`when:`** routing block (all non-empty
conditions must match — AND logic).

The blocks below are **generated from the config source** — for each kind, exactly the
fields it accepts, with each field's meaning, allowed values, and whether it's required.

--8<-- "docs/assets/modules/config-reference.md:targets"
