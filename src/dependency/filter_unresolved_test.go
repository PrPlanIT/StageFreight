package dependency

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
)

// The invariant: inability to verify (resolution failure or empty Latest) must
// NEVER classify as "up to date". Unknown is its own state.
func TestSkipReason_UnresolvedNeverUpToDate(t *testing.T) {
	cfg := UpdateConfig{}

	// Registry lookup failed → unresolved.
	d := freshness.Dependency{Name: "chrono", Current: "0.4.31", Ecosystem: freshness.EcosystemCargo,
		ResolutionError: "crates.io lookup failed: timeout"}
	if r := skipReason(d, cfg, nil, nil); !strings.HasPrefix(r, "unresolved") {
		t.Errorf("resolution error must be unresolved, got %q", r)
	}

	// Empty Latest, no error → still unresolved (could not verify).
	d2 := freshness.Dependency{Name: "x", Current: "1.0.0", Ecosystem: freshness.EcosystemCargo}
	if r := skipReason(d2, cfg, nil, nil); !strings.HasPrefix(r, "unresolved") {
		t.Errorf("empty Latest must be unresolved, got %q", r)
	}

	// Verified current (Latest resolved == Current) → up to date.
	d3 := freshness.Dependency{Name: "y", Current: "1.0.0", Latest: "1.0.0", Ecosystem: freshness.EcosystemCargo}
	if r := skipReason(d3, cfg, nil, nil); r != "up to date" {
		t.Errorf("verified-equal must be up to date, got %q", r)
	}
}
