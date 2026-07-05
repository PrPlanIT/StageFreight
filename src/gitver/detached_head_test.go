package gitver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestDetectVersion_DetachedHEADUsesCIBranch pins the CI branch-tag fix: in a
// detached-HEAD checkout (how CI checks out a commit rather than a branch ref),
// git can't name the branch, so DetectVersionWithOpts must fall back to the
// branch the runner exports (SF_CI_BRANCH). Without this, {branch} in tags
// renders empty on branch builds — producing dev--<sha> and a shared
// latest-dev- that collides across every branch.
func TestDetectVersion_DetachedHEADUsesCIBranch(t *testing.T) {
	dir := t.TempDir()
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
	// Detach HEAD the way CI does — check out the commit by hash, not the branch.
	if err := wt.Checkout(&git.CheckoutOptions{Hash: c1}); err != nil {
		t.Fatalf("detach checkout: %v", err)
	}

	// The runner exports the branch even though git HEAD is now detached.
	t.Setenv("CI_COMMIT_BRANCH", "")
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("SF_CI_BRANCH", "chore/docker-sdk-v29")

	opts := &VersioningOpts{
		TagSources:    []TagSource{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}},
		BranchRules:   []BranchRule{{ID: "default", IsDefault: true, BaseFromIDs: []string{"stable"}, Format: "{base}-dev+{sha}"}},
		NoLineageMode: "error",
	}
	v, err := DetectVersionWithOpts(dir, opts)
	if err != nil {
		t.Fatalf("DetectVersionWithOpts: %v", err)
	}
	if v.Branch != "chore/docker-sdk-v29" {
		t.Errorf("Branch = %q, want %q (detached HEAD must fall back to SF_CI_BRANCH)", v.Branch, "chore/docker-sdk-v29")
	}
}
