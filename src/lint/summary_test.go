package lint

import "testing"

func TestSummarize(t *testing.T) {
	s := Summarize([]Finding{
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed},
		{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}, // review-required, still blocks
		{Severity: SeverityWarning},
		{Severity: SeverityInfo},
	})
	// Both criticals block (secure-by-default) — confidence does not exempt gating.
	if s.Total != 4 || s.Critical != 2 || s.Warning != 1 || s.Info != 1 || s.Blocking != 2 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.GateError() == nil {
		t.Error("blocking criticals → GateError must be non-nil")
	}
	if got := s.CriticalNote(); got != "2 critical" {
		t.Errorf("CriticalNote = %q", got)
	}

	// A lone heuristic critical now blocks (review-required).
	if Summarize([]Finding{{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}}).GateError() == nil {
		t.Error("a heuristic critical must block (review-required)")
	}
}
