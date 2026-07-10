package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestResidualVulnerabilities(t *testing.T) {
	deps := []supplychain.Dependency{
		{Name: "github.com/docker/docker", Current: "v28.5.2", Vulnerabilities: []supplychain.VulnInfo{
			{ID: "CVE-A", Severity: "CRITICAL"},
			{ID: "CVE-B", Severity: "HIGH"},
		}},
		{Name: "lodash", Current: "4.17.20", Vulnerabilities: []supplychain.VulnInfo{
			{ID: "CVE-C", Severity: "LOW"},
		}},
	}
	// CVE-A fixed by an applied update; CVE-B, CVE-C remain unremediated.
	result := &UpdateResult{Applied: []AppliedUpdate{{CVEsFixed: []string{"CVE-A"}}}}

	// fail_on=high → only CVE-B (high) is residual (CVE-A fixed, CVE-C below).
	if res := ResidualVulnerabilities(deps, result, "high"); len(res) != 1 || res[0].VulnID != "CVE-B" {
		t.Fatalf("fail_on=high: want [CVE-B], got %+v", res)
	}
	// fail_on=off → no gate.
	if ResidualVulnerabilities(deps, result, "off") != nil {
		t.Error("fail_on=off must not gate")
	}
	// fail_on=low → CVE-B + CVE-C residual (both at/above low; CVE-A fixed).
	if res := ResidualVulnerabilities(deps, result, "low"); len(res) != 2 {
		t.Errorf("fail_on=low: want 2 residual, got %d: %+v", len(res), res)
	}
	// remediate=false (nil result → nothing applied): every at/above vuln is residual.
	if res := ResidualVulnerabilities(deps, nil, "critical"); len(res) != 1 || res[0].VulnID != "CVE-A" {
		t.Errorf("no updates: want [CVE-A] at critical, got %+v", res)
	}
}
