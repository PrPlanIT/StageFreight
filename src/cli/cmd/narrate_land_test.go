package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLandTree_PlacesContentAtDestination pins the extract-nesting safeguard: a transport
// archive nests the built tree under the artifact basename, so landing must descend into
// that single dir and place its CONTENT at the destination — not destination/<basename>/.
// This is the placement landBuildTree performs after ResolveSuccessfulBuildOutput+Extract.
func TestLandTree_PlacesContentAtDestination(t *testing.T) {
	// Simulate an extraction: a temp dir containing a single nested "sf-references" dir.
	extracted := t.TempDir()
	nested := filepath.Join(extracted, "sf-references")
	mustWriteFile(t, filepath.Join(nested, "cli-reference.md"), "cli")
	mustWriteFile(t, filepath.Join(nested, "sub", "config-reference.md"), "cfg")

	src := descendSingleDir(extracted)
	if src != nested {
		t.Fatalf("descendSingleDir = %q, want the single nested dir %q", src, nested)
	}

	dest := filepath.Join(t.TempDir(), "docs", "reference")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDirInto(src, dest); err != nil {
		t.Fatal(err)
	}

	// Content must land AT dest, not dest/sf-references.
	if _, err := os.Stat(filepath.Join(dest, "cli-reference.md")); err != nil {
		t.Errorf("cli-reference.md not at destination root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "config-reference.md")); err != nil {
		t.Errorf("nested file not preserved under destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sf-references")); err == nil {
		t.Error("content wrongly nested under destination/<basename>")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
