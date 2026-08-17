package build

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/version"
)

// versionedRepo initializes a git repo with one tagged commit on main and returns
// the dir plus a config carrying a versioning scheme, so DetectVersion resolves a
// concrete Version/SHA from the working tree.
func versionedRepo(t *testing.T) (dir string, cfg *config.Config) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("f"); err != nil {
		t.Fatal(err)
	}
	c1, err := wt.Commit("c", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := repo.CreateTag("v1.0.0", c1, nil); err != nil {
		t.Fatalf("tag: %v", err)
	}

	cfg = &config.Config{}
	cfg.Git.Tags = config.OrderedTagSources{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}}
	cfg.Git.Versioning.BranchBuilds = config.OrderedBranchBuilds{
		{ID: "default", BaseFrom: []string{"stable"}, Format: "{base}-dev+{sha}"},
	}
	return dir, cfg
}

// The regression that shipped image dev-35a2d0f with revision 46846c2: OCI labels and
// the banner read the version.* globals — the RUNNING orchestrator binary's ldflags
// stamp — instead of the source under build. ResolveImageStamp must derive from the
// working tree (the SAME path that feeds the compiled binary's ldflags), so its result
// equals DetectVersion and is the fresh HEAD, never the compiled-in global.
func TestResolveImageStamp_DerivesFromSourceNotOrchestratorGlobal(t *testing.T) {
	dir, cfg := versionedRepo(t)

	gotVer, gotCommit := ResolveImageStamp(dir, cfg)

	// Single source of truth: identical to what autoInjectBuildArgs feeds ldflags.
	di, err := DetectVersion(dir, cfg)
	if err != nil || di == nil {
		t.Fatalf("DetectVersion: v=%v err=%v", di, err)
	}
	if gotVer != di.Version || gotCommit != di.SHA {
		t.Fatalf("ResolveImageStamp = (%q, %q), want DetectVersion (%q, %q)",
			gotVer, gotCommit, di.Version, di.SHA)
	}

	// It must be the source HEAD, never the orchestrator's compiled-in stamp — the
	// exact confusion that stamped a stale ancestor as the artifact revision.
	if gotCommit == version.Commit {
		t.Fatalf("resolved commit = orchestrator global %q; must be the working-tree HEAD", version.Commit)
	}
	if gotCommit == "" || len(gotCommit) > 7 {
		t.Fatalf("resolved commit %q is not a short HEAD sha", gotCommit)
	}
}

// With no version scheme resolvable (nil config → DetectVersion errs), the resolver
// falls back to the orchestrator stamp so an unversioned build is never left blank —
// preserving prior behavior for repos that never claimed a version.
func TestResolveImageStamp_FallsBackToGlobalWhenUnversioned(t *testing.T) {
	gotVer, gotCommit := ResolveImageStamp(t.TempDir(), nil)
	if gotVer != version.Version || gotCommit != version.Commit {
		t.Fatalf("fallback = (%q, %q), want globals (%q, %q)",
			gotVer, gotCommit, version.Version, version.Commit)
	}
}
