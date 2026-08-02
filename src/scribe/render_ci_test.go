package scribe

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestRenderCIStencil covers the four run-state producers: rows from recorded
// state, self-bounding at limit (+K more), section defaulting to the stencil id,
// and empty state rendering nothing (so the embed line elides).
func TestRenderCIStencil(t *testing.T) {
	st := &cistate.State{Version: 1}
	st.RecordSubsystem(cistate.SubsystemState{
		Name: "test", Attempted: true, Outcome: "failed", Reason: "3 of 142 tests failed",
		Results: map[string]string{"failures": "TestA, TestB, TestC"},
	})
	st.RecordSubsystem(cistate.SubsystemState{Name: "mirror", Attempted: true, Outcome: "failed", Reason: "sync refused"})
	st.RecordSubsystem(cistate.SubsystemState{Name: "security",
		Results: map[string]string{"blocking_list": "CVE-1 · critical · openssl → 3.0.9\nCVE-2 · high · zlib"}})
	st.RecordSubsystem(cistate.SubsystemState{Name: "build",
		Results: map[string]string{"artifacts": "docs-site (tree)"}})
	st.RecordSubsystem(cistate.SubsystemState{Name: "changelog",
		Results: map[string]string{"breaking": "stencils: shared text-composition library\nconfig: reshape narrate"}})

	ci := func(id string, limit int) string {
		return renderCIStencil(config.StencilDef{ID: id, Type: "ci", Limit: limit}, st)
	}

	// failures: test rows first, then other failed subsystems with reasons.
	wantFailures := "✗ TestA\n✗ TestB\n✗ TestC\n✗ mirror — sync refused"
	if got := ci("failures", 0); got != wantFailures {
		t.Errorf("failures:\n got %q\nwant %q", got, wantFailures)
	}

	// limit self-bounds with a +K more tail.
	if got := ci("failures", 2); got != "✗ TestA\n✗ TestB\n+2 more" {
		t.Errorf("failures limit: got %q", got)
	}

	if got := ci("vulns", 0); got != "⚠ CVE-1 · critical · openssl → 3.0.9\n⚠ CVE-2 · high · zlib" {
		t.Errorf("vulns: got %q", got)
	}
	if got := ci("artifacts", 0); got != "+ docs-site (tree)" {
		t.Errorf("artifacts: got %q", got)
	}
	if got := ci("changelog", 0); !strings.HasPrefix(got, "⚠ BREAKING · stencils:") {
		t.Errorf("changelog: got %q", got)
	}

	// Empty state → every producer renders nothing (the embed line elides).
	empty := &cistate.State{Version: 1}
	for _, id := range []string{"failures", "vulns", "artifacts", "changelog"} {
		if got := renderCIStencil(config.StencilDef{ID: id, Type: "ci"}, empty); got != "" {
			t.Errorf("%s on empty state: got %q, want \"\"", id, got)
		}
	}
}
