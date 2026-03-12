// Package freshness checks for outdated dependencies across ecosystems:
// Dockerfile base images, pinned tool versions, Go modules, Rust crates,
// npm packages, Alpine APK, Debian/Ubuntu APT, and pip packages.
//
// Each checker extracts structured [Dependency] records from project files,
// resolves the latest available version from upstream registries, and reports
// findings through the lint engine. The same Dependency data is designed to
// feed a future "stagefreight update" command for automated MR creation.
package freshness

import "github.com/PrPlanIT/StageFreight/src/lint"

func init() {
	lint.Register("freshness", func() lint.Module { return newModule() })
}
