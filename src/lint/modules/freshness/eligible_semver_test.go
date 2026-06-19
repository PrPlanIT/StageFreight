package freshness

import "testing"

// The reqwest break: ^0.12.22 must resolve to the latest 0.12.x, NEVER 0.13.x —
// 0.12 → 0.13 is a breaking-major boundary in cargo semver.
func TestLatestEligibleSemver_RespectsCaretBoundary(t *testing.T) {
	avail := []string{"0.12.20", "0.12.22", "0.12.23", "0.13.0", "0.13.4"}
	if got := latestEligibleSemver("0.12.22", avail); got != "0.12.23" {
		t.Errorf("^0.12.22 eligible = %q, want 0.12.23 (in-range, not 0.13.x)", got)
	}
	if got := latestEligibleSemver("1.2.3", []string{"1.2.3", "1.9.0", "2.0.0"}); got != "1.9.0" {
		t.Errorf("^1.2.3 eligible = %q, want 1.9.0 (< 2.0.0)", got)
	}
	if got := latestEligibleSemver("0.12.22", []string{"0.13.0"}); got != "" {
		t.Errorf("nothing in range → empty, got %q", got)
	}
}

func TestDependency_UpdateTargetIsCompatible(t *testing.T) {
	// Compatible target known → that's the autonomous target; the major is held.
	d := Dependency{Current: "0.12.22", Latest: "0.13.4", LatestEligible: "0.12.23"}
	if d.UpdateTarget() != "0.12.23" {
		t.Errorf("UpdateTarget = %q, want compatible 0.12.23", d.UpdateTarget())
	}
	if !d.MajorAvailable() {
		t.Error("MajorAvailable must be true — 0.13.4 is out of range")
	}
	// No compatibility model (e.g. exact go.mod pin) → target = latest available.
	g := Dependency{Current: "1.0.0", Latest: "1.2.0"}
	if g.UpdateTarget() != "1.2.0" || g.MajorAvailable() {
		t.Errorf("no-eligible: target=%q major=%v, want 1.2.0/false", g.UpdateTarget(), g.MajorAvailable())
	}
}
