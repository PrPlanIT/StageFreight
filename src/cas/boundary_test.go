package cas

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewFSStoreIsTestOnly enforces the CAS lifecycle boundary as a STRUCTURAL
// invariant, not a remembered grep: no production (non-_test.go) file may
// construct a content store via NewFSStore.
//
// NewFSStore takes an arbitrary path, so it could root the CAS outside the job
// workspace — a shared runner volume, a persistent CI cache, a "reuse the store
// across jobs" optimization. That is exactly the cross-run/cross-tenant footgun
// the workspace-scoping rule forbids. The only sanctioned lifecycle constructor
// is NewWorkspaceStore, which derives the path from the workspace root and so
// cannot be relocated.
//
// NewFSStore stays exported for tests (inert stores at temp dirs). But the
// moment it reappears on a production path this fails — converting "I checked
// the call sites once" into a guarantee that holds under future refactors
// (shared-cache experiments, perf optimizations, a new tool path passing a
// non-workspace root). Grep proves call sites; this proves impossibility.
func TestNewFSStoreIsTestOnly(t *testing.T) {
	srcRoot, err := filepath.Abs("..") // package dir is src/cas; scan all of src/
	if err != nil {
		t.Fatal(err)
	}

	var offenders []string
	var sawDecl bool // proves the scan actually reached the cas package
	walkErr := filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(srcRoot, path)
		inCASPkg := strings.HasPrefix(rel, "cas"+string(filepath.Separator))

		for _, line := range strings.Split(string(data), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") || code == "" {
				continue // comments may legitimately name the function
			}
			if strings.Contains(code, "func NewFSStore(") {
				sawDecl = true
				continue // the declaration itself, not a call site
			}
			// External callers qualify it; in-package callers would not.
			if strings.Contains(code, "cas.NewFSStore") ||
				(inCASPkg && strings.Contains(code, "NewFSStore(")) {
				offenders = append(offenders, rel)
				break
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if !sawDecl {
		t.Fatal("scan never reached the cas package (wrong working dir?) — guard would pass vacuously")
	}
	if len(offenders) > 0 {
		t.Fatalf("NewFSStore is the unbounded CAS constructor and must stay test-only; "+
			"production code must root the store via NewWorkspaceStore(workspaceRoot). "+
			"Offending production files:\n  %s", strings.Join(offenders, "\n  "))
	}
}
