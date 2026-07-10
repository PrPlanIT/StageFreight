package config

import "testing"

// TestPropagateCooldown: dependency.min_release_age (the owner) flows into the
// freshness lint options (the shared resolver read-path), so both honor it.
func TestPropagateCooldown(t *testing.T) {
	cfg := &Config{}
	cfg.Dependency.MinReleaseAge = "3d"
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Lint.Modules["freshness"].Options["min_release_age"]; got != "3d" {
		t.Errorf("freshness min_release_age = %v, want 3d (propagated from dependency)", got)
	}
}

// TestPropagateCooldownBackCompat: with dependency unset, an existing
// freshness-options value stands (historical home still works).
func TestPropagateCooldownBackCompat(t *testing.T) {
	cfg := &Config{}
	cfg.Lint.Modules = map[string]ModuleConfig{"freshness": {Options: map[string]any{"min_release_age": "7d"}}}
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Lint.Modules["freshness"].Options["min_release_age"]; got != "7d" {
		t.Errorf("freshness value must stand when dependency unset (back-compat), got %v", got)
	}
}
