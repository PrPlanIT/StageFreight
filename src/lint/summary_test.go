package lint

import "testing"

func TestSummarize(t *testing.T) {
	s := Summarize([]Finding{
		{Severity: SeverityCritical, Confidence: ConfidenceConfirmed}, // blocking
		{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}, // critical impact, non-blocking
		{Severity: SeverityWarning},
		{Severity: SeverityInfo},
	})
	if s.Total != 4 || s.Critical != 2 || s.Warning != 1 || s.Info != 1 || s.Blocking != 1 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.GateError() == nil {
		t.Error("1 blocking finding → GateError must be non-nil")
	}
	if got := s.CriticalNote(); got != "2 critical, 1 low-confidence non-blocking" {
		t.Errorf("CriticalNote = %q", got)
	}

	// Only a heuristic critical → surfaced but must NOT gate.
	heur := Summarize([]Finding{{Severity: SeverityCritical, Confidence: ConfidenceHeuristic}})
	if heur.GateError() != nil {
		t.Error("only heuristic critical → must not gate")
	}
	if got := heur.CriticalNote(); got != "1 critical, 1 low-confidence non-blocking" {
		t.Errorf("CriticalNote = %q", got)
	}

	// All confirmed → plain note, gates.
	conf := Summarize([]Finding{{Severity: SeverityCritical}})
	if conf.GateError() == nil {
		t.Error("confirmed critical (zero-value confidence) must gate")
	}
	if got := conf.CriticalNote(); got != "1 critical" {
		t.Errorf("CriticalNote = %q", got)
	}
}
