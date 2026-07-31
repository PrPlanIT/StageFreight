package ci

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestNarrateFacet_PresenceEmitsStage guards the behavior repoint (compiler can't): the
// narrate CI stage is emitted when narrate is configured, and NOT when it's empty. A
// wrong predicate would silently drop the stage.
func TestNarrateFacet_PresenceEmitsStage(t *testing.T) {
	has := func(cfg *config.Config) bool {
		for _, f := range DetectActive(cfg) {
			if f.Name == "narrate" {
				return true
			}
		}
		return false
	}

	if has(&config.Config{}) {
		t.Error("narrate stage emitted for an empty config (should be presence-gated)")
	}

	withScribe := &config.Config{
		Scribe: config.ScribeConfig{
			Files: config.OrderedFiles{
				{ID: "readme", File: "README.md"},
			},
		},
	}
	if !has(withScribe) {
		t.Error("narrate stage NOT emitted despite scribe.files configured")
	}

	// The stencils library is presence-NEUTRAL: a stencils-only config (no scribe
	// files/commit) must NOT emit the stage — the library is shared, not a phase (R7).
	stencilsOnly := &config.Config{
		Stencils: config.OrderedStencils{{ID: "build", Label: "build", Output: "b.svg"}},
	}
	if has(stencilsOnly) {
		t.Error("narrate stage should NOT emit for a stencils-only config (library is presence-neutral)")
	}
}
