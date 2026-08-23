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
	"github.com/PrPlanIT/StageFreight/src/version"
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

// The OCI image labels describe the ARTIFACT: image.version/.revision track the built
// repo's source version (DetectVersion), NOT the orchestrator binary. This is the correct
// half of the 0fc1930 provenance fix and must continue to hold.
func TestImageLabels_TrackRepoSource(t *testing.T) {
	dir, cfg := bannerRepo(t)

	di, err := build.DetectVersion(dir, cfg)
	if err != nil || di == nil {
		t.Fatalf("DetectVersion: v=%v err=%v", di, err)
	}

	sfVer, sfCommit := build.ResolveImageStamp(dir, cfg)
	labels := build.StandardLabels("planhash", sfVer, sfCommit, "crucible-verified", "")
	if labels[build.LabelVersion] != di.Version {
		t.Errorf("label %s = %q, want repo source %q", build.LabelVersion, labels[build.LabelVersion], di.Version)
	}
	if labels[build.LabelRevision] != di.SHA {
		t.Errorf("label %s = %q, want repo source %q", build.LabelRevision, labels[build.LabelRevision], di.SHA)
	}
}

// The StageFreight identity line (logo banner + slim stamp) names the TOOL: its version is
// the orchestrator binary's OWN ldflags (version.Version), independent of the repo under
// build. The repo's code identity lives in the ── Code ── block, not this line. Guards
// against the 0fc1930 regression that fed the "StageFreight" line the built repo's version.
func TestIdentityInfo_IsToolVersion(t *testing.T) {
	dir, cfg := bannerRepo(t)

	if got := IdentityInfo().Version; got != version.Version {
		t.Errorf("IdentityInfo().Version = %q, want orchestrator version.Version %q", got, version.Version)
	}

	// It must NOT track the built repo's source version — that is the artifact/label concern.
	di, err := build.DetectVersion(dir, cfg)
	if err != nil || di == nil {
		t.Fatalf("DetectVersion: v=%v err=%v", di, err)
	}
	if di.Version == version.Version {
		t.Skip("repo source version coincidentally equals the tool version; decoupling assertion not meaningful")
	}
	if IdentityInfo().Version == di.Version {
		t.Errorf("identity version %q tracks the built repo source; it must be the tool's own version", di.Version)
	}
}
