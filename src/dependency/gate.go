package dependency

import (
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/vulnerability/severity"
)

// ResidualVuln is a vulnerability still present AFTER the update pass — its
// advisory was not fixed by any applied update — at or above the gate threshold.
type ResidualVuln struct {
	Dep      string
	Version  string
	VulnID   string
	Severity string
}

// ResidualVulnerabilities is the deps module's Policy stage: over the residual
// findings (vulnerabilities whose advisory id was NOT fixed by any Applied
// update — a held major, no fix available, or ignored), return those at or above
// the failOn threshold. failOn "off"/"" or an unrecognized label → no gate (nil).
// Same evaluation shape as the other modules, in the vulnerability severity scale.
//
// remediate=false shows up naturally: with nothing applied, no advisory is fixed,
// so every vulnerability at/above the threshold is residual.
func ResidualVulnerabilities(deps []supplychain.Dependency, result *UpdateResult, failOn string) []ResidualVuln {
	minRank := severity.Rank(severity.Normalize(failOn))
	if minRank == 0 { // "off" / unrecognized → no gate
		return nil
	}

	fixed := map[string]bool{}
	if result != nil {
		for _, a := range result.Applied {
			for _, id := range a.CVEsFixed {
				fixed[id] = true
			}
		}
	}

	var out []ResidualVuln
	for _, d := range deps {
		for _, v := range d.Vulnerabilities {
			if v.ID == "" || fixed[v.ID] {
				continue
			}
			if severity.Rank(severity.Normalize(v.Severity)) >= minRank {
				out = append(out, ResidualVuln{Dep: d.Name, Version: d.Current, VulnID: v.ID, Severity: v.Severity})
			}
		}
	}
	return out
}
