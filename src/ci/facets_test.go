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

	withNarrate := &config.Config{
		Narrate: config.NarrateConfig{
			Badges: []config.BadgeConfig{{ID: "build", Text: "build", Output: "b.svg"}},
		},
	}
	if !has(withNarrate) {
		t.Error("narrate stage NOT emitted despite narrate.badges configured")
	}
}
