package lint

import "testing"

func TestGateErrorSince(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed, Module: "osv", Message: "pre-existing CVE", File: "a"},
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed, Module: "osv", Message: "new CVE", File: "b"},
		{Severity: SeverityCritical, Confidence: ConfidenceHeuristic, Module: "secrets", Message: "maybe key", File: "c"},
	}

	// Only a NEW blocking finding fails the baseline gate.
	newBlocking := map[string]bool{findings[1].Fingerprint(): true}
	if GateErrorSince(findings, newBlocking, "main") == nil {
		t.Error("a new blocking finding must fail the baseline gate")
	}

	// Pre-existing blocking findings (none new) do NOT fail — known debt stays non-blocking.
	if err := GateErrorSince(findings, map[string]bool{}, "main"); err != nil {
		t.Errorf("pre-existing blocking findings must not fail the baseline gate: %v", err)
	}

	// A new but heuristic (non-blocking) finding does not fail either.
	newHeuristic := map[string]bool{findings[2].Fingerprint(): true}
	if err := GateErrorSince(findings, newHeuristic, "main"); err != nil {
		t.Errorf("a new heuristic (non-blocking) finding must not fail: %v", err)
	}
}
