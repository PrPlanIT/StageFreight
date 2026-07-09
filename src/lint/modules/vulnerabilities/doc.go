// Package vulnerabilities is the single lint module that reports known
// vulnerabilities affecting a project's dependencies. It renders a canonical
// supply-chain analysis.Assessment — one finding per advisory — so a CVE
// observed by multiple sources (the OSV-API correlation attached to the
// dependency Snapshot, plus an osv-scanner run) is reported exactly once with
// one verdict. It supersedes the freshness module's vulnerability findings and
// the standalone osv module, which produced the duplicate reports.
package vulnerabilities

import "github.com/PrPlanIT/StageFreight/src/lint"

func init() {
	lint.Register("vulnerabilities", func() lint.Module { return newModule() })
}
