package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTempRootNestsOnce(t *testing.T) {
	root := TempRoot()
	if filepath.Dir(root) != os.TempDir() && filepath.Base(os.TempDir()) != filepath.Base(root) {
		t.Fatalf("TempRoot %q is not in %q", root, os.TempDir())
	}
	t.Setenv("TMPDIR", root)
	if got := os.TempDir(); filepath.Base(got) != filepath.Base(root) {
		t.Fatalf("TMPDIR %q", got)
	}
}
