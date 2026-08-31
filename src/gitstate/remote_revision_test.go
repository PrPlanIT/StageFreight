package gitstate

import (
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// seedRemote builds a real bare repo reachable over file:// with a branch and a tag, and
// returns its URL plus a commit function for moving refs afterwards.
func seedRemote(t *testing.T) (url string, wt *git.Worktree, repo *git.Repository) {
	t.Helper()
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	seed := filepath.Join(tmp, "seed")
	url = "file://" + bare

	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	r, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	w, _ := r.Worktree()
	if err := os.WriteFile(filepath.Join(seed, "preset.yml"), []byte("lint:\n  level: full\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add("preset.yml"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Commit("first", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatal(err)
	}
	return url, w, r
}

func headHash(t *testing.T, r *git.Repository) string {
	t.Helper()
	h, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	return h.Hash().String()
}

// RemoteRefRevision is what makes a tracked preset cheap to re-check and a pinned tag
// cheap to verify, so its ref-name resolution is exercised against a real remote rather
// than a stub that would agree with whatever it was told.
func TestRemoteRefRevision(t *testing.T) {
	url, wt, repo := seedRemote(t)
	first := headHash(t, repo)

	branch, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	branchName := branch.Name().Short() // whatever go-git initialised (main/master)

	if _, err := repo.CreateTag("v1", branch.Hash(), nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(&git.PushOptions{RemoteName: "origin",
		RefSpecs: []gitconfig.RefSpec{"refs/tags/*:refs/tags/*"}}); err != nil {
		t.Fatal(err)
	}

	t.Run("bare branch name", func(t *testing.T) {
		got, err := RemoteRefRevision(url, branchName)
		if err != nil || got != first {
			t.Fatalf("got %q, %v; want %q", got, err, first)
		}
	})

	t.Run("fully qualified branch", func(t *testing.T) {
		got, err := RemoteRefRevision(url, "refs/heads/"+branchName)
		if err != nil || got != first {
			t.Fatalf("got %q, %v; want %q", got, err, first)
		}
	})

	t.Run("bare tag name", func(t *testing.T) {
		got, err := RemoteRefRevision(url, "v1")
		if err != nil || got != first {
			t.Fatalf("got %q, %v; want %q", got, err, first)
		}
	})

	t.Run("fully qualified tag", func(t *testing.T) {
		got, err := RemoteRefRevision(url, "refs/tags/v1")
		if err != nil || got != first {
			t.Fatalf("got %q, %v; want %q", got, err, first)
		}
	})

	t.Run("a ref that does not exist errors", func(t *testing.T) {
		if _, err := RemoteRefRevision(url, "nope"); err == nil {
			t.Fatal("want an error for an absent ref")
		}
	})

	// The point of the whole mechanism: when the branch advances, the answer changes,
	// so the resolver knows to transfer. If this returned the old id, a tracked preset
	// would silently stop updating.
	t.Run("advancing the branch changes the answer", func(t *testing.T) {
		dir := wt.Filesystem.Root()
		if err := os.WriteFile(filepath.Join(dir, "preset.yml"), []byte("lint:\n  level: none\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("preset.yml"); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit("second", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t"}}); err != nil {
			t.Fatal(err)
		}
		if err := repo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
			t.Fatal(err)
		}
		second := headHash(t, repo)
		if second == first {
			t.Fatal("test did not actually advance the branch")
		}
		got, err := RemoteRefRevision(url, branchName)
		if err != nil || got != second {
			t.Fatalf("got %q, %v; want the new head %q", got, err, second)
		}
	})

	// A moved tag is how a pin is violated, and detecting it without transferring
	// content is the entire reason this exists.
	t.Run("moving the tag changes the answer", func(t *testing.T) {
		second := headHash(t, repo)
		if err := repo.DeleteTag("v1"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateTag("v1", plumbing.NewHash(second), nil); err != nil {
			t.Fatal(err)
		}
		if err := repo.Push(&git.PushOptions{RemoteName: "origin", Force: true,
			RefSpecs: []gitconfig.RefSpec{"+refs/tags/*:refs/tags/*"}}); err != nil {
			t.Fatal(err)
		}
		got, err := RemoteRefRevision(url, "v1")
		if err != nil {
			t.Fatal(err)
		}
		if got == first {
			t.Error("a moved tag still reported its old id — a violated pin would go unnoticed")
		}
		if got != second {
			t.Errorf("got %q, want the tag's new target %q", got, second)
		}
	})
}
