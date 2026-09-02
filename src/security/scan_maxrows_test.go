package security

import (
	"strings"
	"testing"
)

func vulnsOf(n int, sev string) []Vulnerability {
	out := make([]Vulnerability, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Vulnerability{ID: sev + "-" + string(rune('a'+i%26)), Severity: sev, Package: "pkg"})
	}
	return out
}

// "full" must mean complete within reason. A large image carries thousands of advisories
// and every forge caps a release body, rejecting an over-long one outright — so an
// unbounded table costs the entire release rather than the rows past the limit.
func TestBuildFullBody_BoundsTheTable(t *testing.T) {
	result := &ScanResult{Status: "warning", Critical: 2, High: 3, Medium: 40}
	result.Vulnerabilities = append(result.Vulnerabilities, vulnsOf(2, "CRITICAL")...)
	result.Vulnerabilities = append(result.Vulnerabilities, vulnsOf(3, "HIGH")...)
	result.Vulnerabilities = append(result.Vulnerabilities, vulnsOf(40, "MEDIUM")...)

	body := buildFullBody(result, "tile", 10)

	if got := strings.Count(body, "| pkg |"); got != 10 {
		t.Errorf("wrote %d rows, want the bound of 10", got)
	}
	// Worst-first: the bound keeps what a reader acts on.
	if !strings.Contains(body, "**Critical**") {
		t.Error("critical rows must survive the bound")
	}
	if !strings.Contains(body, "and 35 more of lower severity") {
		t.Errorf("the omitted count must be stated, got:\n%s", body)
	}
	// The block must still close, or everything after it collapses into it.
	if !strings.HasSuffix(strings.TrimSpace(body), "</details>") {
		t.Error("the details block must be closed")
	}
}

// 0 restores the previous unbounded behaviour for anyone who wants every row.
func TestBuildFullBody_ZeroMeansUnbounded(t *testing.T) {
	result := &ScanResult{Status: "warning", Medium: 40}
	result.Vulnerabilities = vulnsOf(40, "MEDIUM")

	body := buildFullBody(result, "tile", 0)
	if got := strings.Count(body, "| pkg |"); got != 40 {
		t.Errorf("wrote %d rows, want all 40", got)
	}
	if strings.Contains(body, "and 0 more") {
		t.Error("nothing was omitted, so nothing should be claimed omitted")
	}
}
