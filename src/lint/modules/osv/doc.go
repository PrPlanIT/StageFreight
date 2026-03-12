// Package osv runs osv-scanner against lockfiles to detect known
// vulnerabilities from the OSV database. The scanner binary is optional;
// if not present in PATH the module silently produces no findings.
package osv

import "github.com/PrPlanIT/StageFreight/src/lint"

func init() {
	lint.Register("osv", func() lint.Module { return newModule() })
}
