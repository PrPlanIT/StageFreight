package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTaggingRename: tag: → tagging: (hard break — the old key no longer parses).
func TestTaggingRename(t *testing.T) {
	if _, _, err := LoadWithWarnings(writeCfg(t, "version: 1\ntagging: {}\n")); err != nil {
		t.Fatalf("tagging: should load: %v", err)
	}
	if _, _, err := LoadWithWarnings(writeCfg(t, "version: 1\ntag: {}\n")); err == nil {
		t.Fatal("retired tag: key should fail (hard break)")
	}
}

// TestSigningProfilesMerge: signing_profiles: (list) → signing.profiles: (map),
// folded into the internal Signing list; the old key no longer parses.
func TestSigningProfilesMerge(t *testing.T) {
	cfg, _, err := LoadWithWarnings(writeCfg(t,
		"version: 1\nsigning:\n  profiles:\n    release: { requires: keyless }\n"))
	if err != nil {
		t.Fatalf("signing.profiles should load: %v", err)
	}
	// NormalizeSigning may append a synthetic "legacy" profile, so check for presence
	// of the declared one rather than exact length.
	var found bool
	for _, p := range cfg.Signing {
		if p.ID == "release" {
			found = true
		}
	}
	if !found {
		t.Fatalf("signing.profiles not folded into Signing: %+v", cfg.Signing)
	}
	if _, _, err := LoadWithWarnings(writeCfg(t,
		"version: 1\nsigning_profiles:\n  - { id: release, requires: keyless }\n")); err == nil {
		t.Fatal("retired signing_profiles: key should fail (hard break)")
	}
}
