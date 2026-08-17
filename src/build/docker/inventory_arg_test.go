package docker

import (
	"os"
	"path/filepath"
	"testing"
)

// findBase returns the first inventoried base image with the given name.
func findBase(t *testing.T, res *InventoryResult, name string) PackageInfo {
	t.Helper()
	for _, b := range res.BaseImages {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("base image %q not found in %+v", name, res.BaseImages)
	return PackageInfo{}
}

// An ARG-based alpine base must inventory as its concrete version (contents-base badge:
// "alpine 3.23.5"), not the literal ${ALPINE_VERSION}.
func TestExtractInventory_ArgBaseVersionResolved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = "ARG ALPINE_VERSION=3.23.5\nFROM alpine:${ALPINE_VERSION}\nRUN apk add nginx\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	alpine := findBase(t, res, "alpine")
	if alpine.Version != "3.23.5" {
		t.Errorf("alpine base version = %q, want 3.23.5 (resolved, not ${ALPINE_VERSION})", alpine.Version)
	}
}

// The resolver handles all three reference forms in a FROM tag.
func TestExtractInventory_ArgFormsResolved(t *testing.T) {
	cases := map[string]string{
		"braced":        "ARG V=3.23.5\nFROM alpine:${V}\n",
		"inlineDefault": "ARG V=3.23.5\nFROM alpine:${V:-9.9.9}\n",
		"bare":          "ARG V=3.23.5\nFROM alpine:$V\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "Dockerfile")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := ExtractInventory(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := findBase(t, res, "alpine").Version; got != "3.23.5" {
				t.Errorf("alpine version = %q, want 3.23.5", got)
			}
		})
	}
}

// An ENV default resolves a FROM interpolation when no ARG declares the variable.
func TestExtractInventory_EnvBaseVersionResolved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = "ENV BASE_TAG=3.23.5\nFROM alpine:${BASE_TAG}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := findBase(t, res, "alpine").Version; got != "3.23.5" {
		t.Errorf("alpine version = %q, want 3.23.5", got)
	}
}
