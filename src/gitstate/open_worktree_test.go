package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A commit made through OpenRepo() inside a LINKED git worktree must land in the shared
// object store and advance the worktree's branch. go-git needs EnableDotGitCommonDir to
// resolve a worktree's `.git` file (gitdir: …/.git/worktrees/<name>) against the common dir;
// without it the commit is silently MISPLACED — a hash is returned but the branch never moves
// and the object isn't in the shared store. That is exactly how `stagefreight commit` inside
// a worktree reported a phantom sha and left HEAD unchanged.
func TestOpenRepo_LinkedWorktreeCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to create a linked worktree")
	}
	tmp := t.TempDir()
	main := filepath.Join(tmp, "main")
	sig := &object.Signature{Name: "t", Email: "t@t"}

	r, err := git.PlainInit(main, false)
	if err != nil {
		t.Fatal(err)
	}
	mwt, _ := r.Worktree()
	if err := os.WriteFile(filepath.Join(main, "base"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mwt.Add("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := mwt.Commit("base", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}

	// Linked worktree on a new branch (go-git cannot create worktrees; use system git).
	wtDir := filepath.Join(tmp, "wt")
	if out, err := exec.Command("git", "-C", main, "worktree", "add", "-b", "feature", wtDir).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	// Stage a change and commit THROUGH OpenRepo — the `stagefreight commit` path.
	if err := os.WriteFile(filepath.Join(wtDir, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wr, err := OpenRepo(wtDir)
	if err != nil {
		t.Fatalf("OpenRepo(worktree): %v", err)
	}
	wwt, err := wr.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wwt.Add("a"); err != nil {
		t.Fatal(err)
	}
	newHash, err := wwt.Commit("feature commit", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatalf("commit in worktree: %v", err)
	}

	// The worktree's branch must actually point at the new commit (HEAD advanced)...
	wr2, err := OpenRepo(wtDir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := wr2.Head()
	if err != nil {
		t.Fatalf("reading worktree HEAD after commit: %v", err)
	}
	if head.Hash() != newHash {
		t.Fatalf("worktree branch = %s, want %s — the branch ref did not advance (misplaced commit)", head.Hash(), newHash)
	}
	// ...and the commit object must be in the SHARED store (readable from the main repo).
	if _, err := r.CommitObject(newHash); err != nil {
		t.Fatalf("commit %s not in the shared object store — go-git wrote it to the worktree's private dir: %v", newHash, err)
	}
}
