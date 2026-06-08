# Known Issues

## Minor

### `color: auto` falls through to default grey for version strings

`StatusColor()` in `src/badge/engine.go` only maps status keywords (`passing`, `failed`, `warning`, etc.) to colors. When `color: auto` is used on a badge whose value is a version string (e.g., `v0.1.1`), none of the keywords match and it falls back to default grey.

**Workaround:** Use an explicit hex color instead of `auto` for version badges. The release badge currently uses `#74ecbe` (mint).

**Future:** `auto` could be version-aware — stable semver green, prerelease yellow, `0.x.x` teal, etc.

### `docs run` generates but does not publish — local "last mile" is manual

`stagefreight docs run` runs the enabled documentation generators (badges, reference docs, narrator, docker README) and **writes the files**, but it intentionally does **not** commit or push them — its help reads *"Run all enabled documentation generators (without auto-commit — use `ci run docs` for that)."* The commit/publish step lives only in the lifecycle phase runner (`ci run docs` / narrate), driven by `.stagefreight.yml` `docs.commit`.

This makes docs the **odd one out** among the publish-type operations: registry images publish directly via `stagefreight docker build` (pushes by default) and releases via `stagefreight release create`, but **there is no standalone "publish docs" command** — the only way to complete docs locally is to run the CI-phase command (`ci run docs`) or to do the last mile by hand.

**Workaround (local docs generation + publish):**
1. `stagefreight docs run` (or `stagefreight narrator run`) — generate/refresh the files.
2. Review the diff.
3. `stagefreight commit -t docs -m "refresh generated docs"` then `stagefreight push` — the manual "last mile" that `ci run docs` does automatically.

**Future:** give docs a symmetric standalone publish path so the local surface is consistent — either a `--commit` flag on `docs run`, or a dedicated `docs publish`, or fold it into a top-level `stagefreight run` local-pipeline orchestrator (see the local-dev-ergonomics design discussion). Until then, treat `docs run` as generate-only and own the commit/push yourself.
