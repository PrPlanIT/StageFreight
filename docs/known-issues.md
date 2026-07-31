# Known Issues

## Minor

### `color: auto` falls through to default grey for version strings

`StatusColor()` in `src/badge/engine.go` only maps status keywords (`passing`, `failed`, `warning`, etc.) to colors. When `color: auto` is used on a badge whose value is a version string (e.g., `v0.1.1`), none of the keywords match and it falls back to default grey.

**Workaround:** Use an explicit hex color instead of `auto` for version badges. The release badge currently uses `#74ecbe` (mint).

**Future:** `auto` could be version-aware — stable semver green, prerelease yellow, `0.x.x` teal, etc.

### `scribe apply` generates but does not publish — local "last mile" is manual

`stagefreight scribe apply` renders the configured stencils (badges, shields, included fragments) into their files and **writes them**, but it intentionally does **not** commit or push. The commit/publish step runs in CI (the publish phase), driven by `.stagefreight.yml` `scribe.commit`.

This makes scribe the **odd one out** among the publish-type operations: registry images push by default and releases publish via `stagefreight release create`, but **there is no standalone "publish stencils" command** — locally, you complete the last mile by hand.

**Workaround (local generation + publish):**
1. `stagefreight scribe apply` — render/refresh the files.
2. Review the diff.
3. `stagefreight commit -t docs -m "refresh generated docs"` then `stagefreight push`.

**Future:** give scribe a symmetric standalone publish path so the local surface is consistent — a `--commit` flag on `scribe apply`, or fold it into a top-level `stagefreight run` local-pipeline orchestrator. Until then, treat `scribe apply` as generate-only and own the commit/push yourself.
