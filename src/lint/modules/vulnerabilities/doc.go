// Package vulnerabilities is the single lint module that reports known
// vulnerabilities affecting a project's dependencies. Per file it canonicalizes
// advisory observations from two sources — the OSV-API correlation attached to
// the file's dependencies, plus a per-file osv-scanner run — into one finding
// per advisory, so a CVE observed by both is reported exactly once with one
// verdict. It supersedes the freshness module's vulnerability findings and the
// standalone osv module, which produced the duplicate reports. The old "osv"
// module/config key still resolves here (see the lint engine's module aliases).
package vulnerabilities

import "github.com/PrPlanIT/StageFreight/src/lint"

func init() {
	lint.Register("vulnerabilities", func() lint.Module { return newModule() })
}
