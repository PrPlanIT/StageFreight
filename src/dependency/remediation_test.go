package dependency

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestEvaluateRemediation_States(t *testing.T) {
	vuln := func(id, sev, fixedIn string) []supplychain.VulnInfo {
		return []supplychain.VulnInfo{{ID: id, Severity: sev, FixedIn: fixedIn}}
	}
	// remediated: an applied update carried the fix.
	rem := supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Vulnerabilities: vuln("GHSA-a", "HIGH", "1.1.0")}
	result := &UpdateResult{Applied: []AppliedUpdate{{CVEsFixed: []string{"GHSA-a"}}}}
	if got := EvaluateRemediation([]supplychain.Dependency{rem}, UpdateConfig{}, result); len(got) != 1 || got[0].State != StateRemediated {
		t.Errorf("remediated: %+v", got)
	}
	// no-fix: advisory has no fixed-in.
	nf := supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Vulnerabilities: vuln("GHSA-b", "HIGH", "")}
	if got := EvaluateRemediation([]supplychain.Dependency{nf}, UpdateConfig{}, nil); got[0].State != StateNoFix {
		t.Errorf("no-fix: %+v", got)
	}
	// blocked-by-policy: cargo exact pin blocks the fix — unremediable under declared policy.
	bp := supplychain.Dependency{Name: "c", Ecosystem: supplychain.EcosystemCargo, Current: "1.8.0", Constraint: "=1.8.0", Vulnerabilities: vuln("GHSA-c", "CRITICAL", "1.9.0")}
	got := EvaluateRemediation([]supplychain.Dependency{bp}, UpdateConfig{}, nil)
	if got[0].State != StateBlockedByPolicy || got[0].BlockedBy != PolicyNativeConstraint {
		t.Errorf("blocked-by-policy: %+v", got)
	}
	// reachable-unapplied: fix reachable under policy but nothing applied.
	ru := supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Vulnerabilities: vuln("GHSA-d", "HIGH", "1.1.0")}
	if got := EvaluateRemediation([]supplychain.Dependency{ru}, UpdateConfig{}, nil); got[0].State != StateReachableUnapplied {
		t.Errorf("reachable-unapplied: %+v", got)
	}
}

func TestResiduals_GateSet(t *testing.T) {
	// residual = state != remediated, at/above failOn. A remediated one is excluded.
	deps := []supplychain.Dependency{
		{Name: "a", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Vulnerabilities: []supplychain.VulnInfo{{ID: "R", Severity: "CRITICAL", FixedIn: "2.0.0"}}},
		{Name: "b", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Vulnerabilities: []supplychain.VulnInfo{{ID: "F", Severity: "CRITICAL", FixedIn: "1.1.0"}}},
	}
	result := &UpdateResult{Applied: []AppliedUpdate{{CVEsFixed: []string{"F"}}}}
	res := Residuals(EvaluateRemediation(deps, UpdateConfig{}, result), "high")
	if len(res) != 1 || res[0].VulnID != "R" {
		t.Errorf("residual set = %+v, want only R", res)
	}
}

func TestRemediationSummaryMarkdown(t *testing.T) {
	if RemediationSummaryMarkdown(nil) != "" {
		t.Error("no advisories must render empty (no section)")
	}
	evals := []RemediationEvaluation{
		{VulnID: "GHSA-a", Severity: "HIGH", Package: "foo", Version: "1.0.0", FixedIn: "1.1.0", State: StateRemediated},
		{VulnID: "GHSA-c", Severity: "CRITICAL", Package: "bar", Version: "1.8.0", FixedIn: "1.9.0", State: StateBlockedByPolicy, BlockedBy: PolicyNativeConstraint, Detail: "=1.8.0"},
		{VulnID: "GHSA-d", Severity: "LOW", Package: "baz", Version: "2.0.0", State: StateNoFix},
	}
	md := RemediationSummaryMarkdown(evals)
	for _, want := range []string{
		"## Security remediation", "Remediated", "GHSA-a", "fixed in 1.1.0",
		"Unremediable under declared policy", "GHSA-c", "excluded by native_constraint (=1.8.0)",
		"No fix available", "GHSA-d",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}
