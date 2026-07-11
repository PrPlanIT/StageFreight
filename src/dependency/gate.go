package dependency

import (
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// ResidualVuln is a vulnerability still present AFTER the update pass — its
// advisory was not fixed by any applied update — at or above the gate threshold.
type ResidualVuln struct {
	Dep      string
	Version  string
	VulnID   string
	Severity string
}

// ResidualVulnerabilities is the deps module's Policy stage: the residual
// vulnerabilities (not fixed by any Applied update) at or above the failOn threshold.
// It is the simple []ResidualVuln view over the richer RemediationEvaluation model
// (EvaluateRemediation + Residuals) — behavior-identical: the residual SET is the
// state != remediated advisories. Callers wanting the WHY use EvaluateRemediation.
// failOn "off"/"" or an unrecognized label → no gate (nil).
func ResidualVulnerabilities(deps []supplychain.Dependency, result *UpdateResult, failOn string) []ResidualVuln {
	var out []ResidualVuln
	for _, e := range Residuals(EvaluateRemediation(deps, UpdateConfig{}, result), failOn) {
		out = append(out, ResidualVuln{Dep: e.Package, Version: e.Version, VulnID: e.VulnID, Severity: e.Severity})
	}
	return out
}
