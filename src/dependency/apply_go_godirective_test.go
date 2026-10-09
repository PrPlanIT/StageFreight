package dependency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGoMod(t *testing.T, dir, goVer string) string {
	t.Helper()
	p := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(p, []byte("module example.com/x\n\ngo "+goVer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSyncGoDirectives_NoChangeNotApplied guards the untrustworthy-report fix: when the
// runner leaves go.mod byte-identical (e.g. an older toolchain reverts the bump), the
// result must record a Skipped no-op, NOT a phantom Applied "stdlib" update — the exact
// bug where deps printed "Applied stdlib 1.23 -> 1.26.9" with "files changed 0".
func TestSyncGoDirectives_NoChangeNotApplied(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "1.23")

	noop := func(ctx context.Context, d string, args ...string) ([]byte, error) {
		return nil, nil // edit+tidy that does not persist the directive
	}
	result := &UpdateResult{}
	resolved := goDirectiveSyncResult{Targets: []goDirectiveSyncTarget{{ModuleDir: ".", GoVersion: "1.26.9"}}}

	if err := syncGoDirectivesFromResolved(context.Background(), dir, result, resolved, noop); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("no-op must NOT be reported as applied, got %d: %+v", len(result.Applied), result.Applied)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Category != SkipNoChange {
		t.Fatalf("no-op must be a single SkipNoChange, got %+v", result.Skipped)
	}
	if len(result.TouchedModuleDirs) != 0 {
		t.Fatalf("no-op must not touch any module dir, got %v", result.TouchedModuleDirs)
	}
}

// TestSyncGoDirectives_RealChangeApplied: when the runner actually rewrites the directive,
// it is recorded as applied and the module dir is marked touched.
func TestSyncGoDirectives_RealChangeApplied(t *testing.T) {
	dir := t.TempDir()
	modFile := writeGoMod(t, dir, "1.23")

	effective := func(ctx context.Context, d string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "mod" && args[1] == "edit" {
			ver := strings.TrimPrefix(args[2], "-go=")
			if err := os.WriteFile(modFile, []byte("module example.com/x\n\ngo "+ver+"\n"), 0o644); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	result := &UpdateResult{}
	resolved := goDirectiveSyncResult{Targets: []goDirectiveSyncTarget{{ModuleDir: ".", GoVersion: "1.26.9"}}}

	if err := syncGoDirectivesFromResolved(context.Background(), dir, result, resolved, effective); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("real change must be applied once, got %d: %+v", len(result.Applied), result.Applied)
	}
	if got := result.Applied[0]; got.OldVer != "1.23" || got.NewVer != "1.26.9" || got.Dep.Name != "stdlib" {
		t.Fatalf("applied entry wrong: %+v", got)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("real change must not be skipped, got %+v", result.Skipped)
	}
	if len(result.TouchedModuleDirs) != 1 || result.TouchedModuleDirs[0] != "." {
		t.Fatalf("real change must touch the module dir, got %v", result.TouchedModuleDirs)
	}
}

// TestSyncGoDirectives_AlreadyAtTarget: directive already equals target → nothing recorded
// and the runner is never invoked.
func TestSyncGoDirectives_AlreadyAtTarget(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "1.26.9")

	called := false
	runner := func(ctx context.Context, d string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	result := &UpdateResult{}
	resolved := goDirectiveSyncResult{Targets: []goDirectiveSyncTarget{{ModuleDir: ".", GoVersion: "1.26.9"}}}

	if err := syncGoDirectivesFromResolved(context.Background(), dir, result, resolved, runner); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if called {
		t.Fatal("runner must not be invoked when the directive is already at target")
	}
	if len(result.Applied) != 0 || len(result.Skipped) != 0 {
		t.Fatalf("already-at-target must record nothing, applied=%v skipped=%v", result.Applied, result.Skipped)
	}
}
