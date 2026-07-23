package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writePresetFixture lays a .stagefreight.yml (+ an optional preset file) in a temp
// dir and returns the config path.
func writePresetFixture(t *testing.T, cfg, presetName, preset string) string {
	t.Helper()
	dir := t.TempDir()
	if preset != "" {
		if err := os.WriteFile(filepath.Join(dir, presetName), []byte(preset), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPresetsResolveOnRunPath is THE split-brain guard: LoadWithWarnings (the runtime
// load path) must APPLY a section preset, not merely carry the ref. Before loadResolved,
// this failed — the run path decoded without resolving, so the reporter showed presets
// applied while builds ignored them.
func TestPresetsResolveOnRunPath(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nsecurity:\n  preset: ./sec.yml\n",
		"sec.yml", "security:\n  output: PRESET_OK\n")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Security.OutputDir; got != "PRESET_OK" {
		t.Fatalf("preset NOT applied on the run path: security.output = %q, want %q", got, "PRESET_OK")
	}
}

// TestPresetLocalSiblingOverrides confirms the existing DeepMerge layering is honored
// end to end: a local sibling next to preset: wins over the preset value.
func TestPresetLocalSiblingOverrides(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nsecurity:\n  preset: ./sec.yml\n  output: LOCAL_WINS\n",
		"sec.yml", "security:\n  output: FROM_PRESET\n")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Security.OutputDir; got != "LOCAL_WINS" {
		t.Fatalf("local sibling did not override preset: security.output = %q, want %q", got, "LOCAL_WINS")
	}
}

// TestPresetSourceDecodes guards governed-config loadability: governance injects a
// top-level preset_source: block; under KnownFields(true) it must be a known field
// (decode), not a rejected unknown one.
func TestPresetSourceDecodes(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\npreset_source:\n  provider: gitlab\n  ref: abc123\n  cache_policy: authoritative\n",
		"", "")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("governed config with preset_source failed to load: %v", err)
	}
	if cfg.PresetSource == nil || cfg.PresetSource.Ref != "abc123" {
		t.Fatalf("preset_source not decoded: %+v", cfg.PresetSource)
	}
}

// TestPresetFreeConfigUnchanged is the regression guard: a config with no presets
// loads exactly as before — decoded from its ORIGINAL bytes, no map round-trip.
func TestPresetFreeConfigUnchanged(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nsecurity:\n  output: PLAIN\n  enabled: true\n", "", "")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Security.OutputDir != "PLAIN" {
		t.Fatalf("preset-free config altered: security.output = %q", cfg.Security.OutputDir)
	}
}
