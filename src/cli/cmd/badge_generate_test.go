package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestHasConfiguredBadges gates the narrate phase: a project with no badge items must
// report false so docs generation SKIPS badges instead of failing. A project that
// declares top-level badges: reports true.
func TestHasConfiguredBadges(t *testing.T) {
	// No badges configured — the static-site case that was failing narrate.
	if hasConfiguredBadges(&config.Config{}) {
		t.Error("hasConfiguredBadges(empty) = true, want false (nothing to generate ⇒ skip)")
	}

	// Top-level badges: declared — real work to do.
	withBadges := &config.Config{
		Badges: config.BadgesConfig{
			Items: []config.BadgeConfig{{ID: "build", Text: "build", Output: ".stagefreight/badges/build.svg"}},
		},
	}
	if !hasConfiguredBadges(withBadges) {
		t.Error("hasConfiguredBadges(with badges:) = false, want true")
	}
}
