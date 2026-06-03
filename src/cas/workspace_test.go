package cas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceStoreIsWorkspaceScoped is the guard against the cross-tenant
// footgun: the content store must ALWAYS live inside the given workspace root.
// If someone ever relocates the CAS to a shared/runner volume, this fails — which
// is the point. The store's safety (cleanup can't reach another pipeline) rests
// entirely on this being true.
func TestWorkspaceStoreIsWorkspaceScoped(t *testing.T) {
	ws := t.TempDir()
	root := NewWorkspaceStore(ws).Root()

	wsPrefix := filepath.Clean(ws) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(root)+string(filepath.Separator), wsPrefix) {
		t.Fatalf("store root %q is not under workspace %q — the CAS escaped its workspace", root, ws)
	}
	if got, want := root, WorkspaceObjectsDir(ws); got != want {
		t.Fatalf("store root %q != WorkspaceObjectsDir %q (path derivation diverged)", got, want)
	}
}

// TestRetireIsWorkspaceScoped proves Retire deletes only the calling workspace's
// store: retire A, and B's store must survive untouched. This is the concurrency
// safety property — two pipelines on one runner cannot delete each other.
func TestRetireIsWorkspaceScoped(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, ws := range []string{a, b} {
		dir := WorkspaceObjectsDir(ws)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Retire(a); err != nil {
		t.Fatalf("Retire(a): %v", err)
	}
	if _, err := os.Stat(WorkspaceObjectsDir(a)); !os.IsNotExist(err) {
		t.Errorf("workspace A store not retired (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(WorkspaceObjectsDir(b), "marker")); err != nil {
		t.Errorf("retiring A wrongly affected workspace B: %v", err)
	}

	// Idempotent: retiring an already-gone store is a no-op.
	if err := Retire(a); err != nil {
		t.Errorf("second Retire(a) should be a no-op, got %v", err)
	}
}
