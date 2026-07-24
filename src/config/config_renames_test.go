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

// TestBranchBuildsDefaultOrderFree: default is a named catch-all, valid at ANY
// position (was: must be last). Here default is declared FIRST.
func TestBranchBuildsDefaultOrderFree(t *testing.T) {
	body := "version: 1\n" +
		"git:\n" +
		"  branches:\n    main: \"^main$\"\n" +
		"  tags:\n    stable: { pattern: \"^v.*\" }\n" +
		"  versioning:\n" +
		"    branch_builds:\n" +
		"      default: { base_from: [stable], format: \"{base}-dev\" }\n" +
		"      release: { match: main, base_from: [stable], format: \"{base}\" }\n"
	if _, _, err := LoadWithWarnings(writeCfg(t, body)); err != nil {
		t.Fatalf("default-first branch_builds should validate (order-free): %v", err)
	}
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

// TestPresentationDissolve: presentation.commit → commit.render (pointer-guarded so
// defaults survive), and the retired presentation: key no longer parses.
func TestPresentationDissolve(t *testing.T) {
	cfg, _, err := LoadWithWarnings(writeCfg(t,
		"version: 1\ncommit:\n  render:\n    preserve_raw_subject: false\n"))
	if err != nil {
		t.Fatalf("commit.render should load: %v", err)
	}
	if cfg.Commit.Render.PreserveRawSubject {
		t.Fatalf("commit.render override not applied: %+v", cfg.Commit.Render)
	}
	// A partial render: block overlays defaults — the untouched field keeps its default.
	if !cfg.Commit.Render.EnforceConventional {
		t.Fatalf("partial render should preserve sibling default EnforceConventional: %+v", cfg.Commit.Render)
	}
	// Default preserved when render is absent.
	def, _, err := LoadWithWarnings(writeCfg(t, "version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !def.Commit.Render.PreserveRawSubject {
		t.Fatal("default commit.render lost when render absent")
	}
	if _, _, err := LoadWithWarnings(writeCfg(t, "version: 1\npresentation: {}\n")); err == nil {
		t.Fatal("retired presentation: key should fail (hard break)")
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
	for _, p := range cfg.SigningSetup.Profiles {
		if p.ID == "release" {
			found = true
		}
	}
	if !found {
		t.Fatalf("signing.profiles not decoded: %+v", cfg.SigningSetup.Profiles)
	}
	if _, _, err := LoadWithWarnings(writeCfg(t,
		"version: 1\nsigning_profiles:\n  - { id: release, requires: keyless }\n")); err == nil {
		t.Fatal("retired signing_profiles: key should fail (hard break)")
	}
}
