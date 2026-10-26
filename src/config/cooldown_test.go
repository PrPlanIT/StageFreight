package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

const day = 24 * time.Hour

// TestPropagateCooldown: dependency.min_release_age (the owner) flows into the
// freshness lint options (the shared resolver read-path), so both honor it.
func TestPropagateCooldown(t *testing.T) {
	cfg := &Config{}
	cfg.Dependency.MinReleaseAge = &supplychain.Cooldown{Major: 14 * day, Minor: 3 * day, Patch: 3 * day}
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Lint.Modules["freshness"].Options["min_release_age"]; got != *cfg.Dependency.MinReleaseAge {
		t.Errorf("freshness min_release_age = %v, want the dependency value", got)
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

// TestToolchainCooldownCascade: a tool's own min_release_age wins, else
// toolchains.min_release_age; tools inherit nothing when the section sets none.
func TestToolchainCooldownCascade(t *testing.T) {
	src := `
min_release_age: 7d
want:
  trivy: "0.x"
  cosign: {version: 3.x, min_release_age: 0}
  kubectl: {version: 1.34.x, min_release_age: {major: 30d, default: 2d}}
`
	var s ToolchainSection
	if err := yaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Toolchains: s}
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	want := cfg.Toolchains.Want
	if c := want["trivy"].MinReleaseAge; c == nil || c.Patch != 7*day {
		t.Errorf("trivy must inherit the section's 7d, got %+v", c)
	}
	if c := want["cosign"].MinReleaseAge; c == nil || !c.IsZero() {
		t.Errorf("cosign's own 0 must win over the section, got %+v", c)
	}
	if c := want["kubectl"].MinReleaseAge; c == nil || c.Major != 30*day || c.Patch != 2*day {
		t.Errorf("kubectl's own per-size cooldown must win, got %+v", c)
	}

	var bare ToolchainSection
	if err := yaml.Unmarshal([]byte("want:\n  trivy: \"0.x\"\n"), &bare); err != nil {
		t.Fatal(err)
	}
	cfg = &Config{Toolchains: bare}
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if c := cfg.Toolchains.Want["trivy"].MinReleaseAge; c != nil {
		t.Errorf("with no section cooldown a tool inherits none (the dependency default applies), got %+v", c)
	}
}

func TestToolchainCooldownParseError(t *testing.T) {
	var s ToolchainSection
	err := yaml.Unmarshal([]byte("min_release_age: soon\n"), &s)
	if err == nil || !strings.Contains(err.Error(), "toolchains.min_release_age") {
		t.Errorf("a bad section cooldown must name toolchains.min_release_age, got %v", err)
	}
	err = yaml.Unmarshal([]byte("want:\n  trivy: {version: 0.x, min_release_age: {weekly: 1d}}\n"), &s)
	if err == nil || !strings.Contains(err.Error(), "toolchains.want.trivy") || !strings.Contains(err.Error(), "weekly") {
		t.Errorf("a bad tool cooldown must name the tool and key, got %v", err)
	}
}
