# Narrate

The Narrate phase composes repo-facing content — README badges, shields, and included
fragments — and commits it back. Presence-enabled: configure `narrate` and it runs.

| Key | What it does |
|---|---|
| `narrate` | Badge/shield generation, content items (`kind: badge · shield · text · component · props · break · include`), placement rules, and the commit that lands them. |

## Two badge systems

StageFreight has **two** ways to put a badge in your README, and they're easy to confuse.
Both are narrator item kinds and can sit on the same line:

| | `kind: badge` | `kind: props` |
|---|---|---|
| **What** | A local SVG generator you **own** | A registry of **external** provider badges |
| **Powered by** | the `badge` package (renders SVG) | the `props` package (resolver/composer) |
| **Output** | committed `.svg` files — branded, version-stamped | markdown → an external URL with live data |
| **Sources** | your own data | shields.io, codecov, Go Report Card, docker-pulls, SLSA… |
| **CLI** | `stagefreight badge generate` | `stagefreight props list` · `props render` |
| **Use when** | you control the data and want branded assets | you want live ecosystem data in a standard format |

In short: **`badge` = "I'll draw my own." `props` = "give me the shields.io one for docker
pulls."** Both compose through the narrator, so they coexist happily in one README.

!!! tip "Discovering props"
    `stagefreight props categories` and `stagefreight props list` enumerate every available
    prop type (e.g. `docker-pulls`, `codecov`, `slsa`). Each is a typed, validated resolver —
    unknown types and params are rejected at run time, not silently ignored.

## Reference

--8<-- "docs/modules/config-reference.md:narrate"
