package toolchain

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestResolveVersion_WildcardLock(t *testing.T) {
	// The resolved version of a wildcard now lives in .stagefreight/toolchains.lock under
	// rootDir, not in the config. Seed a lock, then resolve.
	root := t.TempDir()
	lock := &Lock{}
	lock.Set("trivy", "0.69.3", "") // wildcard trivy is locked to 0.69.3
	if err := WriteLock(root, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	desired := map[string]config.ToolConstraint{
		"trivy": {Constraint: "0.69.x"}, // wildcard — resolves from the lock
		"syft":  {Constraint: "1.42.3"}, // exact — the constraint IS the version
		"grype": {Constraint: "1.0.x"},  // wildcard, no lock entry
	}
	if v, pinned := ResolveVersion(root, "trivy", "", desired); v != "0.69.3" || !pinned {
		t.Errorf("wildcard+lock → %q pinned=%v, want 0.69.3/true", v, pinned)
	}
	if v, pinned := ResolveVersion(root, "syft", "", desired); v != "1.42.3" || !pinned {
		t.Errorf("exact → %q pinned=%v, want 1.42.3/true", v, pinned)
	}
	// Unlocked wildcard: must NEVER return the un-downloadable wildcard string; it falls
	// through to the tool default.
	if v, _ := ResolveVersion(root, "grype", "", desired); v == "1.0.x" {
		t.Errorf("unlocked wildcard returned the wildcard string %q (must fall through to default)", v)
	}
	// An explicit request always wins, lock or no lock.
	if v, _ := ResolveVersion(root, "trivy", "9.9.9", desired); v != "9.9.9" {
		t.Errorf("explicit request → %q, want 9.9.9", v)
	}
}
