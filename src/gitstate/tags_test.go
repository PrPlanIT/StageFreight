package gitstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestTagPointsAtHEAD pins the channel-ref guard: TagPointsAtHEAD checks a
// SPECIFIC tag, so a co-located channel ref (dev-*) on the same HEAD commit does
// not mask a real release tag. A tag on an earlier commit, or a missing tag,
// does not point at HEAD.
func TestTagPointsAtHEAD(t *testing.T) {
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
	commit := func(b byte) plumbing.Hash {
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte{b}, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("f"); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit("c", &git.CommitOptions{
			Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
		})
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
		return h
	}

	c1 := commit(1)
	if _, err := repo.CreateTag("v1.0.0", c1, nil); err != nil {
		t.Fatalf("tag v1.0.0: %v", err)
	}
	c2 := commit(2) // becomes HEAD
	// Two tags co-located at HEAD: a release tag AND a channel ref.
	if _, err := repo.CreateTag("v1.2.3", c2, nil); err != nil {
		t.Fatalf("tag v1.2.3: %v", err)
	}
	if _, err := repo.CreateTag("dev-abc12345", c2, nil); err != nil {
		t.Fatalf("tag dev-abc12345: %v", err)
	}

	check := func(tag string, want bool) {
		t.Helper()
		got, err := TagPointsAtHEAD(repo, tag)
		if err != nil {
			t.Fatalf("TagPointsAtHEAD(%q): %v", tag, err)
		}
		if got != want {
			t.Errorf("TagPointsAtHEAD(%q) = %v, want %v", tag, got, want)
		}
	}
	check("v1.2.3", true)       // release tag at HEAD — found...
	check("dev-abc12345", true) // ...even though a channel ref is co-located
	check("v1.0.0", false)      // on the earlier commit
	check("nonexistent", false) // missing tag
}

// TestIsEmptyCommit pins the changelog no-op guard: a commit whose tree matches its sole
// parent (e.g. one a rebase/replay re-applied when the change was already present) is
// reported empty and skipped from release notes, while a content-bearing commit and the
// root commit are not.
func TestIsEmptyCommit(t *testing.T) {
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
	sig := &object.Signature{Name: "t", Email: "t@t", When: time.Now()}
	obj := func(h plumbing.Hash) *object.Commit {
		c, err := repo.CommitObject(h)
		if err != nil {
			t.Fatalf("commit object: %v", err)
		}
		return c
	}

	// Root commit (with content): not empty (no parent to compare).
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("f"); err != nil {
		t.Fatal(err)
	}
	root, err := wt.Commit("root", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatalf("root commit: %v", err)
	}
	if IsEmptyCommit(obj(root)) {
		t.Errorf("root commit reported empty")
	}

	// Content-bearing child: not empty.
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("f"); err != nil {
		t.Fatal(err)
	}
	real, err := wt.Commit("real change", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatalf("real commit: %v", err)
	}
	if IsEmptyCommit(obj(real)) {
		t.Errorf("content-bearing commit reported empty")
	}

	// No-op commit (tree identical to parent): empty.
	empty, err := wt.Commit("no-op", &git.CommitOptions{Author: sig, AllowEmptyCommits: true})
	if err != nil {
		t.Fatalf("empty commit: %v", err)
	}
	if !IsEmptyCommit(obj(empty)) {
		t.Errorf("no-op commit NOT reported empty")
	}
}
