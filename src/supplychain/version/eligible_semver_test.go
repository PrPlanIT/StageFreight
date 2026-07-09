package version

import "testing"

// The reqwest break: ^0.12.22 must resolve to the latest 0.12.x, NEVER 0.13.x —
// 0.12 → 0.13 is a breaking-major boundary in cargo semver.
func TestLatestEligibleSemver_RespectsCaretBoundary(t *testing.T) {
	avail := []string{"0.12.20", "0.12.22", "0.12.23", "0.13.0", "0.13.4"}
	if got := LatestEligibleSemver("0.12.22", avail); got != "0.12.23" {
		t.Errorf("^0.12.22 eligible = %q, want 0.12.23 (in-range, not 0.13.x)", got)
	}
	if got := LatestEligibleSemver("1.2.3", []string{"1.2.3", "1.9.0", "2.0.0"}); got != "1.9.0" {
		t.Errorf("^1.2.3 eligible = %q, want 1.9.0 (< 2.0.0)", got)
	}
	if got := LatestEligibleSemver("0.12.22", []string{"0.13.0"}); got != "" {
		t.Errorf("nothing in range → empty, got %q", got)
	}
}
