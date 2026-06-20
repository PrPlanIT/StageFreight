package osv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDominatedLockfile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("# root"), 0o644)
	sub := filepath.Join(root, "crates", "rqlite-rs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sub, "Cargo.lock"), []byte("# vendored sub-lock"), 0o644)

	if isDominatedLockfile(filepath.Join(root, "Cargo.lock"), "Cargo.lock") {
		t.Error("root Cargo.lock must NOT be dominated — it is the build graph")
	}
	if !isDominatedLockfile(filepath.Join(sub, "Cargo.lock"), "Cargo.lock") {
		t.Error("nested vendored Cargo.lock must be dominated (skipped)")
	}
}
