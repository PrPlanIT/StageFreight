package security

import "testing"

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

func TestSeverityRank(t *testing.T) {
	if SeverityRank("CRITICAL") <= SeverityRank("high") {
		t.Error("critical must outrank high")
	}
	if SeverityRank("moderate") != SeverityRank("medium") {
		t.Error("moderate must equal medium (OSV vs CVSS vocab)")
	}
	if SeverityRank("") != 0 || SeverityRank("nonsense") != 0 {
		t.Error("unknown severity must rank 0")
	}
}
