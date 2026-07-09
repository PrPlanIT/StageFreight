package supplychain

import "testing"

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
