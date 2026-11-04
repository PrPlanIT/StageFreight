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

// A symlink in a build tree archives its target's content only when the target is a
// file inside the tree; one reaching outside, at a directory, or nowhere fails the
// archive instead of publishing what it points at.
func TestCreateArchive_DirectorySymlinks(t *testing.T) {
	archive := func(tree string) (*ArchiveResult, error) {
		return CreateArchive(ArchiveOpts{
			Format: "tar.gz", OutputDir: t.TempDir(), NameTemplate: "site-{os}-{arch}",
			BinaryPath: tree, BinaryName: "dist", Target: Target{OS: "linux", Arch: "amd64"},
		})
	}
	newTree := func() string {
		tree := filepath.Join(t.TempDir(), "dist")
		if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTreeFile(t, filepath.Join(tree, "index.html"), "<html>")
		return tree
	}

	tree := newTree()
	if err := os.Symlink("index.html", filepath.Join(tree, "home.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := archive(tree); err != nil {
		t.Fatalf("an in-tree file symlink must archive: %v", err)
	}

	secret := filepath.Join(t.TempDir(), "secret")
	writeTreeFile(t, secret, "token")
	for name, target := range map[string]string{"outside": secret, "directory": "sub", "dangling": "missing"} {
		tree := newTree()
		if err := os.Symlink(target, filepath.Join(tree, "link")); err != nil {
			t.Fatal(err)
		}
		if _, err := archive(tree); err == nil {
			t.Errorf("%s symlink: archive must fail", name)
		}
	}
}
