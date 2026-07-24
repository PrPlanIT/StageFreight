package security

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

func TestGatingCountExcusesUnreachable(t *testing.T) {
	result := &ScanResult{
		Critical: 2, High: 1,
		Vulnerabilities: []Vulnerability{
			{ID: "CVE-1", Severity: "CRITICAL"},
			{ID: "CVE-2", Severity: "CRITICAL"},
			{ID: "CVE-3", Severity: "HIGH"},
		},
	}
	// cross-surface reconciliation proved CVE-2 unreachable
	cs := &CrossSurfaceResult{Vulnerabilities: []analysis.Vulnerability{
		{ID: "CVE-2", Evidence: []evidence.Evidence{evidence.ReachabilityEvidence{State: evidence.ReachUnreachable}}},
	}}

	cases := []struct {
		threshold, policy string
		csArg             *CrossSurfaceResult
		want              int
	}{
		{"critical", "fail", cs, 2},  // policy fail → nothing excused
		{"critical", "pass", nil, 2}, // nil cs → nothing excused
		{"critical", "pass", cs, 1},  // CVE-2 excused → 1 critical remains
		{"high", "pass", cs, 2},      // base 3 (2 crit + 1 high) − CVE-2 → 2
		{"off", "pass", cs, 0},       // no gate
	}
	for _, c := range cases {
		if got := GatingCount(result, c.csArg, c.threshold, c.policy); got != c.want {
			t.Errorf("GatingCount(%s, %s) = %d, want %d", c.threshold, c.policy, got, c.want)
		}
	}
}

func TestCountAtOrAbove(t *testing.T) {
	r := &ScanResult{Critical: 1, High: 2, Medium: 3, Low: 4}
	cases := map[string]int{
		"critical": 1,  // 1
		"high":     3,  // 1+2
		"medium":   6,  // 1+2+3
		"low":      10, // 1+2+3+4
		"off":      0,
		"":         0,
		"bogus":    0,
	}
	for threshold, want := range cases {
		if got := CountAtOrAbove(r, threshold); got != want {
			t.Errorf("CountAtOrAbove(%q) = %d, want %d", threshold, got, want)
		}
	}
}
