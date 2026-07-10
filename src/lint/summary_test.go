package lint

import "testing"

func TestSummarize(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed},
		{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}, // review-required, still blocks
		{Severity: SeverityWarning},
		{Severity: SeverityInfo},
	}
	// Default threshold ("critical") — both criticals block; confidence does not exempt gating.
	s := Summarize(findings, "critical")
	if s.Total != 4 || s.Critical != 2 || s.Warning != 1 || s.Info != 1 || s.Blocking != 2 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.GateError() == nil {
		t.Error("blocking criticals → GateError must be non-nil")
	}
	if got := s.CriticalNote(); got != "2 critical" {
		t.Errorf("CriticalNote = %q", got)
	}

	// A lone heuristic critical still blocks (review-required).
	if Summarize([]Finding{{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}}, "critical").GateError() == nil {
		t.Error("a heuristic critical must block (review-required)")
	}
}

// TestSummarizeFailOnThreshold: the per-module fail_on threshold blocks at the
// configured importance tier (lint's own vocabulary), and "off" disables the gate.
func TestSummarizeFailOnThreshold(t *testing.T) {
	findings := []Finding{{Severity: SeverityCritical}, {Severity: SeverityWarning}, {Severity: SeverityInfo}}
	cases := map[string]int{
		"critical": 1, // critical only
		"warning":  2, // warning + critical
		"info":     3, // everything
		"":         1, // default = critical
		"off":      0, // no gate
	}
	for failOn, wantBlocking := range cases {
		if s := Summarize(findings, failOn); s.Blocking != wantBlocking {
			t.Errorf("Summarize(fail_on=%q).Blocking = %d, want %d", failOn, s.Blocking, wantBlocking)
		}
	}
	if Summarize(findings, "off").GateError() != nil {
		t.Error("fail_on=off must not gate")
	}
	if Summarize(findings, "warning").GateError() == nil {
		t.Error("fail_on=warning with a warning present must gate")
	}
}
