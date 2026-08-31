package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// A stale preset_source: block must fail with a migration hint, not an unknown-field
// error: every governed satellite carries one until it is reconciled, so this is the
// message an operator meets first.
func TestPresetSourceIsRejectedWithGuidance(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\npreset_source:\n  provider: gitlab\n  ref: abc123\n  cache_policy: authoritative\n",
		"", "")

	_, _, err := LoadWithWarnings(path)
	if err == nil {
		t.Fatal("a config carrying preset_source must not load")
	}
	for _, want := range []string{"preset_source", "reconcile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestPresetPreservesScribeContentOrder is THE order-preservation guard for the
// node-based resolver: a scribe.content map composed via a preset keeps DOCUMENT order
// (zebra, apple, mango — deliberately non-alphabetical) end-to-end through the full
// load path. A map[string]any round-trip would alphabetize it to [apple mango zebra].
func TestPresetPreservesScribeContentOrder(t *testing.T) {
	preset := "stencils:\n" +
		"  zebra: { label: zebra, output: z.svg }\n" +
		"  apple: { label: apple, output: a.svg }\n" +
		"  mango: { label: mango, output: m.svg }\n"
	cfg := "version: 1\n" +
		"stencils:\n" +
		"  preset: ./badges.yml\n" +
		"scribe:\n" +
		"  files:\n" +
		"    readme:\n" +
		"      file: README.md\n" +
		"      between: [\"<!-- s -->\", \"<!-- e -->\"]\n" +
		"      items: [zebra, apple, mango]\n"

	path := writePresetFixture(t, cfg, "badges.yml", preset)
	loaded, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var ids []string
	for _, c := range loaded.Stencils {
		ids = append(ids, c.ID)
	}
	if got := fmt.Sprintf("%v", ids); got != "[zebra apple mango]" {
		t.Fatalf("stencils order lost through preset: got %v, want [zebra apple mango]", ids)
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

// A local sibling that the preset does NOT declare must survive resolution. Overriding
// a key the preset already has is the covered case; ADDING one is the shape a repo uses
// to opt into a policy its shared preset says nothing about — dependency.outdated on a
// repo whose dependency section comes from a fleet preset.
func TestPresetLocalSiblingAddsAbsentKey(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\ndependency:\n  outdated:\n    at: major\n    action: error\n  preset: ./dep.yml\n",
		"dep.yml", "dependency:\n  enabled: true\n  output: .stagefreight/deps\n")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// The preset's own keys still apply.
	if !cfg.Dependency.Enabled {
		t.Error("preset key lost: dependency.enabled should be true")
	}
	// The local-only key must not be dropped.
	if got := cfg.Dependency.Outdated.EffectiveAt(); got != "major" {
		t.Errorf("local sibling absent from preset was DROPPED: outdated.at = %q, want major", got)
	}
	if got := cfg.Dependency.Outdated.EffectiveAction(); got != "error" {
		t.Errorf("outdated.action = %q, want error", got)
	}
}
