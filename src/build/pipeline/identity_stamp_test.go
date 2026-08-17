package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// bannerRepo initializes a tagged single-commit repo on main plus a versioned config,
// so DetectVersion resolves a concrete Version/SHA from the working tree.
func bannerRepo(t *testing.T) (dir string, cfg *config.Config) {
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

// The provenance invariant: for one commit, the identity banner version, the OCI
// image.version/.revision labels, and the ldflags/DetectVersion source value are ALL the
// same string. This is the regression that shipped banner+labels stamped 46846c2 on a
// 35a2d0f build — banner and labels read the orchestrator global while the binary's
// ldflags read the source. IdentityInfoAt and StandardLabels must both track DetectVersion.
func TestIdentityInfoAt_MatchesLabelsAndLdflagsSource(t *testing.T) {
	dir, cfg := bannerRepo(t)

	di, err := build.DetectVersion(dir, cfg)
	if err != nil || di == nil {
		t.Fatalf("DetectVersion: v=%v err=%v", di, err)
	}

	banner := IdentityInfoAt(dir, cfg)
	if banner.Version != di.Version {
		t.Errorf("banner version = %q, want DetectVersion %q", banner.Version, di.Version)
	}

	sfVer, sfCommit := build.ResolveImageStamp(dir, cfg)
	labels := build.StandardLabels("planhash", sfVer, sfCommit, "crucible-verified", "")
	if labels[build.LabelVersion] != di.Version {
		t.Errorf("label %s = %q, want %q", build.LabelVersion, labels[build.LabelVersion], di.Version)
	}
	if labels[build.LabelRevision] != di.SHA {
		t.Errorf("label %s = %q, want %q", build.LabelRevision, labels[build.LabelRevision], di.SHA)
	}

	// Regression guard: the resolved banner version must NOT be the stale orchestrator
	// global that the context-free IdentityInfo() still returns.
	if banner.Version == IdentityInfo().Version {
		t.Errorf("banner version %q equals the orchestrator global; it must derive from source", banner.Version)
	}
}
