package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// recordingModule is a whole-repo module that records the paths CheckAll was handed.
type recordingModule struct {
	name     string
	gotPaths []string
}

func (m *recordingModule) Name() string         { return m.name }
func (m *recordingModule) DefaultEnabled() bool { return true }
func (m *recordingModule) AutoDetect() []string { return nil }
func (m *recordingModule) Check(context.Context, FileInfo) ([]Finding, error) {
	return nil, fmt.Errorf("%s: Check called on a whole-repo module", m.name)
}
func (m *recordingModule) CheckAll(_ context.Context, files []FileInfo) ([]Finding, error) {
	for _, f := range files {
		m.gotPaths = append(m.gotPaths, f.Path)
	}
	return nil, nil
}

// fullRepoRecorder additionally declares it needs the whole repo.
type fullRepoRecorder struct{ *recordingModule }

func (m *fullRepoRecorder) NeedsFullRepo() bool { return true }

// TestFullRepoModuleSeesWholeRepoUnderDelta: a FullRepoModule is handed the entire collected
// repo even when RunWithStats is given a delta-filtered subset, while a plain WholeRepoModule
// sees only the delta. This is the exact distinction the links checker needs — a link broken
// by moving a different file lives in a file that did not itself change, so under
// `--level changed` it is only visible when the module sees every file.
func TestFullRepoModuleSeesWholeRepoUnderDelta(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	full := &fullRepoRecorder{&recordingModule{name: "full"}}
	plain := &recordingModule{name: "plain"}
	e := &Engine{RootDir: dir, Modules: []Module{full, plain}}

	files, err := e.CollectFiles()
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	// Simulate --level changed: only c.md is in the scanned (delta) set.
	var delta []FileInfo
	for _, f := range files {
		if f.Path == "c.md" {
			delta = append(delta, f)
		}
	}
	if len(delta) != 1 {
		t.Fatalf("setup: expected c.md in collected set, got %d files", len(files))
	}

	if _, _, err := e.RunWithStats(context.Background(), delta); err != nil {
		t.Fatalf("RunWithStats: %v", err)
	}

	sort.Strings(full.gotPaths)
	if got := full.gotPaths; len(got) != 3 || got[0] != "a.md" || got[1] != "b.md" || got[2] != "c.md" {
		t.Fatalf("FullRepoModule must see the whole repo under delta, got: %v", got)
	}
	if got := plain.gotPaths; len(got) != 1 || got[0] != "c.md" {
		t.Fatalf("plain WholeRepoModule must see only the delta subset, got: %v", got)
	}
}

// TestFullRepoModuleFallsBackWithoutCollect: when files are supplied without CollectFiles
// (e.g. the baseline base-relint), there is no full set to widen to, so a FullRepoModule
// sees exactly what it was given rather than nothing.
func TestFullRepoModuleFallsBackWithoutCollect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	full := &fullRepoRecorder{&recordingModule{name: "full"}}
	e := &Engine{RootDir: dir, Modules: []Module{full}}
	supplied := []FileInfo{{Path: "a.md", AbsPath: filepath.Join(dir, "a.md")}}
	if _, _, err := e.RunWithStats(context.Background(), supplied); err != nil {
		t.Fatalf("RunWithStats: %v", err)
	}
	if got := full.gotPaths; len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("without a collected full set, must fall back to the supplied set, got: %v", got)
	}
}
