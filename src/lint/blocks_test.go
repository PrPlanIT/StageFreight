package lint

import "testing"

func TestFindingBlocks(t *testing.T) {
	// Secure-by-default: every critical blocks, regardless of confidence.
	cases := []struct {
		sev  Severity
		conf Confidence
		want bool
	}{
		{SeverityCritical, ConfidenceConfirmed, true},
		{SeverityCritical, ConfidenceProbable, true},
		{SeverityCritical, ConfidenceHeuristic, true}, // review-required — blocks until confirmed/suppressed
		{SeverityWarning, ConfidenceConfirmed, false},
		{SeverityInfo, ConfidenceConfirmed, false},
	}
	for _, c := range cases {
		if got := (Finding{Severity: c.sev, Confidence: c.conf}).Blocks(); got != c.want {
			t.Errorf("Blocks(sev=%v conf=%v) = %v, want %v", c.sev, c.conf, got, c.want)
		}
	}
}
