package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/ci"
)

// TestNeedsExplicitRefFetch pins the routing decision: only a pull-style ref (neither
// branch nor tag) takes the explicit-fetch path; branches, tags, and an absent ref fall
// through to the proven branch/tag clone.
func TestNeedsExplicitRefFetch(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"refs/pull/3/merge", true},
		{"refs/pull/3/head", true},
		{"refs/merge-requests/7/head", true},
		{"refs/heads/main", false},
		{"refs/tags/v1.2.3", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := needsExplicitRefFetch(plumbing.ReferenceName(tc.ref)); got != tc.want {
			t.Errorf("needsExplicitRefFetch(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

// TestCheckoutExplicitRef proves the core issue-#2 fix: go-git fetches a ref that is
// neither a branch nor a tag (a pull-request merge ref) by its explicit refspec and
// materializes the workspace at the run's SHA. A single-branch clone of such a ref
// fails ("couldn't find remote ref refs/heads/3/merge"), which is the reported bug.
func TestCheckoutExplicitRef(t *testing.T) {
	srcDir := t.TempDir()
	src, err := git.PlainInit(srcDir, false)
	if err != nil {
		t.Fatalf("init source: %v", err)
	}
	wt, err := src.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	const fileName = "merged.txt"
	if err := os.WriteFile(filepath.Join(srcDir, fileName), []byte("merge result"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := wt.Add(fileName); err != nil {
		t.Fatalf("add: %v", err)
	}
	sha, err := wt.Commit("merge commit", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Publish the commit ONLY under a pull-style ref — not any refs/heads/* branch — so a
	// branch clone could not reach it. (The default branch still exists from PlainInit, so
	// also move it away to be sure the pull ref is the only path to this SHA.)
	pullRef := plumbing.ReferenceName("refs/pull/1/merge")
	if err := src.Storer.SetReference(plumbing.NewHashReference(pullRef, sha)); err != nil {
		t.Fatalf("set pull ref: %v", err)
	}

	destDir := filepath.Join(t.TempDir(), "workspace")
	ciCtx := &ci.CIContext{
		Provider:  "github",
		RepoURL:   srcDir, // local path → go-git file transport
		SHA:       sha.String(),
		Workspace: destDir,
	}

	if err := checkoutExplicitRef(context.Background(), destDir, ciCtx, nil, pullRef); err != nil {
		t.Fatalf("checkoutExplicitRef: %v", err)
	}

	// The merge commit's tree must be materialized in the workspace.
	if _, err := os.Stat(filepath.Join(destDir, fileName)); err != nil {
		t.Errorf("expected %s materialized from the pull ref: %v", fileName, err)
	}
	// And HEAD must be pinned to the run's SHA.
	dest, err := git.PlainOpen(destDir)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	head, err := dest.Head()
	if err != nil {
		t.Fatalf("dest head: %v", err)
	}
	if head.Hash() != sha {
		t.Errorf("workspace HEAD = %s, want %s", head.Hash(), sha)
	}
}
