package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func deps() []supplychain.Dependency {
	return []supplychain.Dependency{
		{Name: "major-behind", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Latest: "2.0.0"},
		{Name: "minor-behind", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Latest: "1.4.0"},
		{Name: "patch-behind", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Latest: "1.0.3"},
		{Name: "current", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Latest: "1.0.0"},
		{Name: "unresolved", Ecosystem: supplychain.EcosystemGoMod, Current: "1.0.0", Latest: ""},
	}
}

func names(items []OutdatedItem) map[string]string {
	out := map[string]string{}
	for _, i := range items {
		out[i.Name] = i.Magnitude
	}
	return out
}

// The threshold includes everything ABOVE it, so `minor` reports minors and majors —
// an operator asking to hear about minors certainly wants to hear about majors.
func TestOutdatedAtOrAboveIncludesHigherMagnitudes(t *testing.T) {
	got := names(OutdatedAtOrAbove(deps(), "minor"))
	if len(got) != 2 || got["major-behind"] != "major" || got["minor-behind"] != "minor" {
		t.Fatalf("at=minor must report the minor AND the major, got %v", got)
	}
	if _, ok := got["patch-behind"]; ok {
		t.Error("a patch gap must not trip a minor threshold")
	}
}

// An unresolved dependency is not a measured gap: reporting it here would conflate
// "could not check" with "behind", which the unresolved skip category already covers.
func TestOutdatedAtOrAboveSkipsUnresolvedAndCurrent(t *testing.T) {
	got := names(OutdatedAtOrAbove(deps(), "patch"))
	for _, absent := range []string{"current", "unresolved"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s must not be reported as outdated, got %v", absent, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("at=patch must report all three real gaps, got %v", got)
	}
}

// An unrecognized threshold disables the gate rather than defaulting to the most
// sensitive setting — a typo must not start failing every build in the fleet.
func TestOutdatedAtOrAboveOffAndUnknownDisable(t *testing.T) {
	for _, at := range []string{"off", "", "MAJORR", "critical"} {
		if got := OutdatedAtOrAbove(deps(), at); len(got) != 0 {
			t.Errorf("at=%q must disable the gate, got %v", at, names(got))
		}
	}
}

// Defaults: no gate, and reporting rather than failing when one is set.
func TestOutdatedGateDefaults(t *testing.T) {
	g := config.OutdatedGate{}
	if g.EffectiveAt() != "off" {
		t.Errorf("unset threshold must default to off, got %q", g.EffectiveAt())
	}
	if g.EffectiveAction() != "warn" {
		t.Errorf("unset action must default to warn, got %q", g.EffectiveAction())
	}
}
