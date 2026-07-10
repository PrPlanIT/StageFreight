package lint

import "testing"

func TestGateErrorSince(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed, Module: "osv", Message: "pre-existing CVE", File: "a"},
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed, Module: "osv", Message: "new CVE", File: "b"},
		{Severity: SeverityWarning, Module: "freshness", Message: "behind", File: "c"},
	}

	// Only a NEW blocking finding fails the baseline gate.
	if GateErrorSince(findings, map[string]bool{findings[1].Fingerprint(): true}, "main", "critical") == nil {
		t.Error("a new blocking finding must fail the baseline gate")
	}
	// Pre-existing blocking findings (none new) do NOT fail — known debt stays non-blocking.
	if err := GateErrorSince(findings, map[string]bool{}, "main", "critical"); err != nil {
		t.Errorf("pre-existing blocking findings must not fail the baseline gate: %v", err)
	}
	// A new WARNING is not blocking → does not fail.
	if err := GateErrorSince(findings, map[string]bool{findings[2].Fingerprint(): true}, "main", "critical"); err != nil {
		t.Errorf("a new warning (non-blocking) must not fail: %v", err)
	}
}
