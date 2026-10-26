package build

import (
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A linked worktree's .git is a file, not a directory; reading .git/config off disk found
// no origin there, and `release create` failed with "no git remote URL found".
func TestDetectGitInfo_LinkedWorktree(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "main")
	r, err := git.PlainInitWithOptions(main, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"https://example.com/org/repo.git"}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := r.Worktree()
	head, err := wt.Commit("base", &git.CommitOptions{AllowEmptyCommits: true, Author: &object.Signature{Name: "t", Email: "t@t"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Storer.SetReference(plumbing.NewHashReference("refs/heads/feature", head)); err != nil {
		t.Fatal(err)
	}

	// A linked worktree as `git worktree add` lays it out: <main>/.git/worktrees/wt holds
	// HEAD, commondir and gitdir; the worktree's .git file points back at it.
	linked := filepath.Join(tmp, "wt")
	admin := filepath.Join(main, ".git", "worktrees", "wt")
	files := map[string]string{
		filepath.Join(admin, "HEAD"):      "ref: refs/heads/feature\n",
		filepath.Join(admin, "commondir"): "../..\n",
		filepath.Join(admin, "gitdir"):    filepath.Join(linked, ".git") + "\n",
		filepath.Join(linked, ".git"):     "gitdir: " + admin + "\n",
	}
	for p, c := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	info := detectGitInfo(linked)
	if info == nil || info.Remote != "https://example.com/org/repo.git" || info.Branch != "feature" {
		t.Fatalf("worktree git info = %+v, want origin https://example.com/org/repo.git on feature", info)
	}
}
