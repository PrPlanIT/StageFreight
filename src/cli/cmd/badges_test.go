package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestHasConfiguredBadges gates the narrate badges producer: a project with no badges
// reports false so the runner skips badge generation instead of failing; a project that
// declares narrate.badges reports true.
func TestHasConfiguredBadges(t *testing.T) {
	if hasConfiguredBadges(&config.Config{}) {
		t.Error("hasConfiguredBadges(empty) = true, want false (nothing to generate ⇒ skip)")
	}

	withBadges := &config.Config{
		Scribe: config.ScribeConfig{
			Content: config.OrderedContent{
				{ID: "build", Label: "build", Output: ".stagefreight/badges/build.svg"},
			},
		},
	}
	if !hasConfiguredBadges(withBadges) {
		t.Error("hasConfiguredBadges(with scribe.content badges) = false, want true")
	}
}
