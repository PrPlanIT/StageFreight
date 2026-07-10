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
		Narrate: config.NarrateConfig{
			Badges: []config.BadgeConfig{{ID: "build", Text: "build", Output: ".stagefreight/badges/build.svg"}},
		},
	}
	if !hasConfiguredBadges(withBadges) {
		t.Error("hasConfiguredBadges(with narrate.badges) = false, want true")
	}
}
