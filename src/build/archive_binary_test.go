package build

import (
	"os"
	"path/filepath"
	"testing"
)

// format: binary is a passthrough — the build's single-file output is carried into DistDir
// as-is (no tar/zip), so a build that already emits a packaged artifact is not double-archived.
func TestCreateArchive_BinaryPassthrough(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "app.tar.gz") // pretend a command build already produced a tarball
	payload := []byte("already-packaged-bytes")
	if err := os.WriteFile(src, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")

	res, err := CreateArchive(ArchiveOpts{
		Format:       "binary",
		OutputDir:    out,
		NameTemplate: "myapp-1.0.0-linux-amd64",
		BinaryPath:   src,
		BinaryName:   "app.tar.gz",
	})
	if err != nil {
		t.Fatalf("CreateArchive binary: %v", err)
	}
	if res.Format != "binary" {
		t.Errorf("format = %q, want binary", res.Format)
	}
	// The template is the full output name — no forced extension appended (no double-archive).
	if base := filepath.Base(res.Path); base != "myapp-1.0.0-linux-amd64" {
		t.Errorf("output name = %q, want the template verbatim with no extension", base)
	}
	// Bytes carried intact — the SHA is over the original payload, not a re-wrapped archive.
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("passthrough altered bytes: got %q, want %q", got, payload)
	}
	if res.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", res.Size, len(payload))
	}
}

// A directory source has no single-file passthrough form — it must error, not silently
// produce something odd.
func TestCreateArchive_BinaryPassthrough_RejectsDir(t *testing.T) {
	dir := t.TempDir()
	treeDir := filepath.Join(dir, "tree")
	if err := os.MkdirAll(treeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := CreateArchive(ArchiveOpts{
		Format:       "binary",
		OutputDir:    filepath.Join(dir, "out"),
		NameTemplate: "x",
		BinaryPath:   treeDir,
	})
	if err == nil {
		t.Fatal("expected error for a directory source with format: binary")
	}
}
