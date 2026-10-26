package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestSelect_Digest: an asserted config digest wins for its exact version and is strict
// about the running platform; otherwise the lock's digest for the version applies.
func TestSelect_Digest(t *testing.T) {
	root := t.TempDir()
	lock := &Lock{}
	lock.Set("trivy", "0.76.1", PlatformKey(""), "lockdigest")
	lock.Set("kubectl", "1.34.2", PlatformKey(""), "kubelockdigest")
	if err := WriteLock(root, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	t.Run("wildcard uses the lock's version and digest", func(t *testing.T) {
		sel, err := Select(root, "trivy", map[string]config.ToolConstraint{"trivy": {Constraint: "0.x"}})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if sel.Version != "0.76.1" || sel.SHA256 != "lockdigest" || !sel.Pinned {
			t.Errorf("got %+v, want 0.76.1 / lockdigest / pinned", sel)
		}
	})

	t.Run("asserted digest wins over the lock", func(t *testing.T) {
		desired := map[string]config.ToolConstraint{"kubectl": {Constraint: "1.34.2", SHA256: map[string]string{PlatformKey(""): "asserted"}}}
		sel, err := Select(root, "kubectl", desired)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if sel.SHA256 != "asserted" {
			t.Errorf("asserted digest must be used, got %q", sel.SHA256)
		}
	})

	t.Run("asserted digest missing this platform fails", func(t *testing.T) {
		desired := map[string]config.ToolConstraint{"kubectl": {Constraint: "1.34.2", SHA256: map[string]string{"plan9/mips": "x"}}}
		_, err := Select(root, "kubectl", desired)
		if err == nil || !strings.Contains(err.Error(), PlatformKey("")) {
			t.Errorf("must fail naming %s, got %v", PlatformKey(""), err)
		}
	})

	t.Run("exact pin without assertion uses the lock digest for that version", func(t *testing.T) {
		sel, err := Select(root, "kubectl", map[string]config.ToolConstraint{"kubectl": {Constraint: "1.34.2"}})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if sel.SHA256 != "kubelockdigest" {
			t.Errorf("lock digest = %q, want kubelockdigest", sel.SHA256)
		}
	})

	t.Run("lock digest never answers for another version", func(t *testing.T) {
		sel, err := Select(root, "kubectl", map[string]config.ToolConstraint{"kubectl": {Constraint: "1.34.5"}})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if sel.SHA256 != "" {
			t.Errorf("a digest for 1.34.2 must not verify 1.34.5, got %q", sel.SHA256)
		}
	})
}

// TestSelect_UnreadableLockFails: an old-version lock fails selection rather than
// silently running the built-in default.
func TestSelect_UnreadableLockFails(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(LockPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(root), []byte(`{"lockfileVersion": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Select(root, "trivy", map[string]config.ToolConstraint{"trivy": {Constraint: "0.x"}})
	if err == nil || !strings.Contains(err.Error(), "lockfileVersion 1") {
		t.Errorf("want the lock version error, got %v", err)
	}
}
