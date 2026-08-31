package config

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// DriftedPresets is what publish acts on, so it must report exactly the references
// whose source moved — not the ones merely fetched, and not a fallback to the retained
// copy, which is the opposite situation.
func TestDriftedPresetsSelectsOnlyMovedSources(t *testing.T) {
	cfg := &Config{presetOutcomes: []presetref.Outcome{
		{Ref: presetref.Parse("https://a.example/x.yml"), Fetched: true},
		{Ref: presetref.Parse("https://b.example/y.yml"), Fetched: true, Drifted: true},
		{Ref: presetref.Parse("https://c.example/z.yml"), Fallback: true},
		{Ref: presetref.Parse("src//p.yml@refs/tags/v1"), Kind: presetref.Pinned},
	}}

	got := cfg.DriftedPresets()
	if len(got) != 1 || got[0].Ref.Raw != "https://b.example/y.yml" {
		t.Fatalf("DriftedPresets() = %+v, want only the moved source", got)
	}
	if len(cfg.PresetOutcomes()) != 4 {
		t.Errorf("PresetOutcomes() lost observations: %d", len(cfg.PresetOutcomes()))
	}
}

// A config with no sourced references reports nothing, so publish stays silent rather
// than committing on every run.
func TestNoOutcomesMeansNothingToRepublish(t *testing.T) {
	cfg := &Config{}
	if len(cfg.DriftedPresets()) != 0 || len(cfg.PresetOutcomes()) != 0 {
		t.Fatal("a config with no sourced presets must report nothing")
	}
}
