package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// A digest-pinned base (image:tag@sha256:…) inventories as its tag; the digest is not
// smeared into the version badge (regression: contents-base showed "3.23.5@sha256:…").
func TestExtractInventory_DigestPinnedVersionStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = "FROM docker.io/library/alpine:3.23.5@sha256:1beb0dc0a51de7ff38e3b5274078a2e0b81113ba5c7535e1a03d5913a5edbda3\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := findBase(t, res, "alpine").Version; got != "3.23.5" {
		t.Errorf("alpine version = %q, want 3.23.5 (digest stripped)", got)
	}
}

// An ARG-interpolated tag carrying a digest still inventories as the bare tag.
func TestExtractInventory_ArgWithDigestVersionStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = "ARG V=3.23.5\nFROM alpine:${V}@sha256:1beb0dc0a51de7ff38e3b5274078a2e0b81113ba5c7535e1a03d5913a5edbda3\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := findBase(t, res, "alpine").Version; got != "3.23.5" {
		t.Errorf("alpine version = %q, want 3.23.5 (arg resolved, digest stripped)", got)
	}
}
