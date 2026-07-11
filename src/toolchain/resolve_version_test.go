package toolchain

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestResolveVersion_WildcardLock(t *testing.T) {
	desired := map[string]config.ToolConstraint{
		"trivy": {Constraint: "0.69.x", Resolved: "0.69.3"}, // wildcard + lock
		"syft":  {Constraint: "1.42.3"},                     // exact
		"grype": {Constraint: "1.0.x"},                      // wildcard, no lock
	}
	if v, pinned := ResolveVersion("trivy", "", desired); v != "0.69.3" || !pinned {
		t.Errorf("wildcard+lock → %q pinned=%v, want 0.69.3/true", v, pinned)
	}
	if v, pinned := ResolveVersion("syft", "", desired); v != "1.42.3" || !pinned {
		t.Errorf("exact → %q pinned=%v, want 1.42.3/true", v, pinned)
	}
	// unlocked wildcard: must NEVER return the un-downloadable wildcard string; falls through.
	if v, _ := ResolveVersion("grype", "", desired); v == "1.0.x" {
		t.Errorf("unlocked wildcard returned the wildcard string %q (must fall through to default)", v)
	}
}
