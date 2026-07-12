# Builds & Tests

How artifacts are produced and gated: container images, binaries, and container-run
commands (docs generators, static-site builds), the cache that speeds them up, and the tests
that gate them. Builds are referenced by targets via `build:`.

The `builds:` blocks below are **generated per kind** (`docker`, `binary`, `command`) —
exactly the fields each accepts.

--8<-- "docs/modules/config-reference.md:builds"

--8<-- "docs/modules/config-reference.md:build_cache"

--8<-- "docs/modules/config-reference.md:test"
