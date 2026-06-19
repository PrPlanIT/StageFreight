package dependency

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleHasGoFiles(t *testing.T) {
	// A Hugo-style content/tooling module: go.mod but no .go source.
	content := t.TempDir()
	os.WriteFile(filepath.Join(content, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644)
	os.WriteFile(filepath.Join(content, "hugo.yaml"), []byte("title: x\n"), 0o644)
	os.MkdirAll(filepath.Join(content, "content"), 0o755)
	os.WriteFile(filepath.Join(content, "content", "i.md"), []byte("# hi\n"), 0o644)
	if moduleHasGoFiles(content) {
		t.Error("content module (no .go) must report no Go files")
	}

	// A real Go module.
	app := t.TempDir()
	os.MkdirAll(filepath.Join(app, "pkg"), 0o755)
	os.WriteFile(filepath.Join(app, "pkg", "p.go"), []byte("package p\n"), 0o644)
	if !moduleHasGoFiles(app) {
		t.Error("module with a .go file must report Go files")
	}

	// A .go inside a dotdir (e.g. caches) must not count.
	dot := t.TempDir()
	os.MkdirAll(filepath.Join(dot, ".cache"), 0o755)
	os.WriteFile(filepath.Join(dot, ".cache", "x.go"), []byte("package x\n"), 0o644)
	if moduleHasGoFiles(dot) {
		t.Error(".go inside a dotdir must be skipped")
	}
}
