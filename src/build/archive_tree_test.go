package build

import (
	"os"
	"path/filepath"
	"testing"
)

// A build whose artifact is a directory (a web bundle, a built app tree) archives
// as cleanly as a single binary: every file under the dir lands in the archive,
// nested under the artifact name so it extracts into its own directory.
func TestCreateArchive_Directory(t *testing.T) {
	repo := t.TempDir()
	tree := filepath.Join(repo, "dist")
	if err := os.MkdirAll(filepath.Join(tree, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, filepath.Join(tree, "index.html"), "<html>")
	writeTreeFile(t, filepath.Join(tree, "assets", "app.js"), "console.log(1)")

	res, err := CreateArchive(ArchiveOpts{
		Format:       "tar.gz",
		OutputDir:    t.TempDir(),
		NameTemplate: "site-{os}-{arch}",
		BinaryPath:   tree,
		BinaryName:   "dist",
		Target:       Target{OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	got := map[string]bool{}
	for _, c := range res.Contents {
		got[c] = true
	}
	for _, want := range []string{"dist/index.html", "dist/assets/app.js"} {
		if !got[want] {
			t.Errorf("archive missing %q; contents=%v", want, res.Contents)
		}
	}
}

func writeTreeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
