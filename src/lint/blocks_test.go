package lint

import "testing"

func TestFindingBlocks(t *testing.T) {
	// zero-value Confidence is Confirmed → a critical with unset confidence still blocks.
	if !(Finding{Severity: SeverityCritical}).Blocks() {
		t.Error("zero-value (Confirmed) critical must block")
	}
	cases := []struct {
		sev  Severity
		conf Confidence
		want bool
	}{
		{SeverityCritical, ConfidenceConfirmed, true},
		{SeverityCritical, ConfidenceProbable, true},
		{SeverityCritical, ConfidenceHeuristic, false}, // critical impact, weak evidence → surfaced, non-blocking
		{SeverityWarning, ConfidenceConfirmed, false},
		{SeverityInfo, ConfidenceConfirmed, false},
	}
	for _, c := range cases {
		if got := (Finding{Severity: c.sev, Confidence: c.conf}).Blocks(); got != c.want {
			t.Errorf("Blocks(sev=%v conf=%v) = %v, want %v", c.sev, c.conf, got, c.want)
		}
	}
}
