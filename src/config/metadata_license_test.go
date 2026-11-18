package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMetadataLicense_FromLicenseFile: an undeclared license is the one LICENSE names; a
// declared one stands.
func TestMetadataLicense_FromLicenseFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("MIT License\n\nPermission is hereby granted, free of charge, to any person obtaining a copy"), 0o644)
	p := filepath.Join(dir, ".stagefreight.yml")
	os.WriteFile(p, []byte("version: 1\n"), 0o644)
	cfg, err := LoadOffline(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metadata.License != "MIT" {
		t.Errorf("license = %q", cfg.Metadata.License)
	}
	os.WriteFile(p, []byte("version: 1\nmetadata: { license: GPL-2.0-or-later }\n"), 0o644)
	if cfg, _ = LoadOffline(p); cfg.Metadata.License != "GPL-2.0-or-later" {
		t.Errorf("declared license = %q", cfg.Metadata.License)
	}
}
