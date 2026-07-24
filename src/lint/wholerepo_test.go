package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// fakePerFile is an ordinary per-file module: the engine fans Check out once per
// file. It records how many times Check ran.
type fakePerFile struct{ calls int32 }

func (f *fakePerFile) Name() string         { return "perfile" }
func (f *fakePerFile) DefaultEnabled() bool { return true }
func (f *fakePerFile) AutoDetect() []string { return nil }
func (f *fakePerFile) Check(_ context.Context, file FileInfo) ([]Finding, error) {
	atomic.AddInt32(&f.calls, 1)
	return []Finding{{File: file.Path, Module: "perfile", Severity: SeverityInfo, Message: "pf"}}, nil
}

// fakeWholeRepo implements WholeRepoModule: the engine must call CheckAll exactly
// once with every eligible file and never call Check.
type fakeWholeRepo struct {
	checkCalled bool
	seen        []string
}

func (f *fakeWholeRepo) Name() string         { return "wholerepo" }
func (f *fakeWholeRepo) DefaultEnabled() bool { return true }
func (f *fakeWholeRepo) AutoDetect() []string { return nil }
func (f *fakeWholeRepo) Check(_ context.Context, _ FileInfo) ([]Finding, error) {
	f.checkCalled = true
	return nil, fmt.Errorf("Check must not be called on a whole-repo module")
}
func (f *fakeWholeRepo) CheckAll(_ context.Context, files []FileInfo) ([]Finding, error) {
	for _, fi := range files {
		f.seen = append(f.seen, fi.Path)
	}
	return []Finding{{File: "REPO", Module: "wholerepo", Severity: SeverityWarning, Message: "wr"}}, nil
}

// makeFiles writes n named files into a temp dir and returns their FileInfo.
func makeFiles(t *testing.T, names ...string) (string, []FileInfo) {
	t.Helper()
	dir := t.TempDir()
	var files []FileInfo
	for _, name := range names {
		abs := filepath.Join(dir, name)
		if err := os.WriteFile(abs, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, FileInfo{Path: name, AbsPath: abs, Size: 6})
	}
	return dir, files
}

// TestWholeRepoDispatch: a whole-repo module runs ONCE over every eligible file
// via CheckAll (Check never called), while a per-file module in the same engine
// still fans Check out per file. Stats and findings from both paths appear.
func TestWholeRepoDispatch(t *testing.T) {
	dir, files := makeFiles(t, "a.txt", "b.txt", "c.txt")
	pf := &fakePerFile{}
	wr := &fakeWholeRepo{}
	engine := &Engine{Config: config.LintConfig{}, RootDir: dir, Modules: []Module{pf, wr}}

	findings, modStats, err := engine.RunWithStats(context.Background(), files)
	if err != nil {
		t.Fatalf("RunWithStats: %v", err)
	}

	if got := atomic.LoadInt32(&pf.calls); got != 3 {
		t.Errorf("per-file Check calls = %d, want 3 (one per file)", got)
	}
	if wr.checkCalled {
		t.Error("whole-repo module's Check was called; engine must route to CheckAll")
	}
	sort.Strings(wr.seen)
	if want := []string{"a.txt", "b.txt", "c.txt"}; !equalStrings(wr.seen, want) {
		t.Errorf("CheckAll saw %v, want %v (all files, once)", wr.seen, want)
	}

	// 3 per-file findings + 1 whole-repo finding.
	if len(findings) != 4 {
		t.Errorf("findings = %d, want 4", len(findings))
	}

	stats := statsByName(modStats)
	if s := stats["wholerepo"]; s.Files != 3 || s.Findings != 1 || s.Warnings != 1 {
		t.Errorf("wholerepo stats = %+v, want Files=3 Findings=1 Warnings=1", s)
	}
	if s := stats["perfile"]; s.Files != 3 || s.Findings != 3 {
		t.Errorf("perfile stats = %+v, want Files=3 Findings=3", s)
	}
}

// TestWholeRepoHonorsModuleExclude: a whole-repo module receives only files that
// pass its own per-module exclude patterns.
func TestWholeRepoHonorsModuleExclude(t *testing.T) {
	dir, files := makeFiles(t, "a.txt", "b.txt", "c.txt")
	wr := &fakeWholeRepo{}
	cfg := config.LintConfig{Modules: map[string]config.ModuleConfig{
		"wholerepo": {Exclude: []string{"b.txt"}},
	}}
	engine := &Engine{Config: cfg, RootDir: dir, Modules: []Module{wr}}

	if _, _, err := engine.RunWithStats(context.Background(), files); err != nil {
		t.Fatalf("RunWithStats: %v", err)
	}
	sort.Strings(wr.seen)
	if want := []string{"a.txt", "c.txt"}; !equalStrings(wr.seen, want) {
		t.Errorf("CheckAll saw %v, want %v (b.txt excluded)", wr.seen, want)
	}
}

// TestWholeRepoCheckGuardErrors: calling Check directly on the whole-repo module
// fails loud, so a mis-dispatch can never silently drop cross-file dedup.
func TestWholeRepoCheckGuardErrors(t *testing.T) {
	wr := &fakeWholeRepo{}
	if _, err := wr.Check(context.Background(), FileInfo{Path: "x"}); err == nil {
		t.Error("whole-repo Check should return an error (mis-dispatch guard)")
	}
}

// fakeWholeRepoPartial returns a finding AND an error together, modelling a
// whole-repo module (like vulnerabilities) that keeps the observations it
// gathered from good files when one file fails.
type fakeWholeRepoPartial struct{}

func (fakeWholeRepoPartial) Name() string         { return "wrpartial" }
func (fakeWholeRepoPartial) DefaultEnabled() bool { return true }
func (fakeWholeRepoPartial) AutoDetect() []string { return nil }
func (fakeWholeRepoPartial) Check(_ context.Context, _ FileInfo) ([]Finding, error) {
	return nil, fmt.Errorf("guard")
}
func (fakeWholeRepoPartial) CheckAll(_ context.Context, _ []FileInfo) ([]Finding, error) {
	return []Finding{{File: "go.mod", Module: "wrpartial", Severity: SeverityCritical, Message: "real CVE"}},
		fmt.Errorf("bad.json: one file failed to observe")
}

// TestWholeRepoPartialFindingsSurviveError is the security-critical contract: a
// whole-repo module that returns partial findings alongside an error must have
// those findings RETAINED (and counted) so the gate — which keys on the findings
// slice, not the returned error — still sees a real critical. Dropping them on
// error would be fail-open.
func TestWholeRepoPartialFindingsSurviveError(t *testing.T) {
	dir, files := makeFiles(t, "a.txt")
	engine := &Engine{Config: config.LintConfig{}, RootDir: dir, Modules: []Module{fakeWholeRepoPartial{}}}

	findings, modStats, err := engine.RunWithStats(context.Background(), files)
	if err == nil {
		t.Error("module error should surface through RunWithStats")
	}
	if len(findings) != 1 || findings[0].Severity != SeverityCritical {
		t.Fatalf("partial findings dropped on error: got %+v, want the critical retained", findings)
	}
	if s := statsByName(modStats)["wrpartial"]; s.Findings != 1 || s.Critical != 1 {
		t.Errorf("stats = %+v, want Findings=1 Critical=1 counted despite the error", s)
	}
}

func statsByName(stats []ModuleStats) map[string]ModuleStats {
	m := make(map[string]ModuleStats, len(stats))
	for _, s := range stats {
		m[s.Name] = s
	}
	return m
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
