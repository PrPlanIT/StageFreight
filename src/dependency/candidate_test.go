package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestConstruct(t *testing.T) {
	// Eligible: an outdated gomod dep → candidate with the natural target.
	cs := Construct(supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod", Current: "1.0.0", Latest: "1.1.0"}, UpdateConfig{}, nil, nil)
	if !cs.Eligible || cs.Target != "1.1.0" {
		t.Errorf("outdated dep: Eligible=%v Target=%q, want true/1.1.0", cs.Eligible, cs.Target)
	}
	// Not eligible: up to date → reproduces the legacy skip category + verbatim reason.
	up := Construct(supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod", Current: "1.0.0", Latest: "1.0.0"}, UpdateConfig{}, nil, nil)
	if up.Eligible || up.Category != SkipUpToDate || up.Reason != "up to date" {
		t.Errorf("up-to-date: Eligible=%v Category=%q Reason=%q", up.Eligible, up.Category, up.Reason)
	}
}

// TestCandidateSet_Admits exercises the version-level predicate + binding layer.
func TestCandidateSet_Admits(t *testing.T) {
	build := func(d supplychain.Dependency, cfg UpdateConfig) CandidateSet { return Construct(d, cfg, nil, nil) }

	p := build(supplychain.Dependency{Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Pinned: "replace directive"}, UpdateConfig{})
	if ok, layer, _ := p.Admits("2.0.0"); ok || layer != PolicyPinned {
		t.Errorf("pinned: ok=%v layer=%q, want false/pinned", ok, layer)
	}

	c := build(supplychain.Dependency{Name: "c", Ecosystem: supplychain.EcosystemCargo, Current: "1.8.0", Constraint: "=1.8.0", Latest: "1.9.0", LatestEligible: "1.8.0"}, UpdateConfig{})
	if ok, _, _ := c.Admits("1.8.0"); !ok {
		t.Error("=1.8.0 must admit 1.8.0")
	}
	if ok, layer, _ := c.Admits("1.9.0"); ok || layer != PolicyNativeConstraint {
		t.Errorf("=1.8.0 vs 1.9.0: ok=%v layer=%q, want false/native_constraint", ok, layer)
	}

	cl := build(supplychain.Dependency{Name: "g", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.3", Latest: "1.3.0"}, UpdateConfig{MaxUpdate: "patch"})
	if ok, _, _ := cl.Admits("1.2.7"); !ok {
		t.Error("patch ceiling must admit 1.2.7")
	}
	if ok, layer, _ := cl.Admits("1.3.0"); ok || layer != PolicyCeiling {
		t.Errorf("patch ceiling vs 1.3.0: ok=%v layer=%q, want false/ceiling", ok, layer)
	}

	pr := build(supplychain.Dependency{Name: "p", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0"}, UpdateConfig{})
	if ok, layer, _ := pr.Admits("2.0.0-rc1"); ok || layer != PolicyPrerelease {
		t.Errorf("prerelease: ok=%v layer=%q, want false/prerelease", ok, layer)
	}
}
