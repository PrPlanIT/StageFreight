package gitstate

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestBranchesContaining(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	sig := &object.Signature{Name: "t", Email: "t@t"}
	commit := func(name string) plumbing.Hash {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
		h, err := wt.Commit(name, &git.CommitOptions{Author: sig})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	set := func(ref string, h plumbing.Hash) {
		t.Helper()
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(ref), h)); err != nil {
			t.Fatal(err)
		}
	}

	base := commit("base")
	set("refs/heads/main", base)
	set("refs/remotes/origin/main", base)
	tip := commit("tip")
	set("refs/heads/feature/x", tip)
	set("refs/remotes/origin/release-1.0", tip)
	set("refs/tags/v1", tip) // tags are not branches

	for _, c := range []struct {
		commit plumbing.Hash
		want   []string
	}{
		{base, []string{"feature/x", "main", "release-1.0"}},
		{tip, []string{"feature/x", "release-1.0"}},
	} {
		got, err := BranchesContaining(repo, c.commit)
		if err != nil {
			t.Fatal(err)
		}
		// HEAD's own branch (master, from PlainInit) also carries tip.
		got = slices.DeleteFunc(got, func(s string) bool { return s == "master" })
		if !slices.Equal(got, c.want) {
			t.Errorf("BranchesContaining(%s) = %v, want %v", c.commit.String()[:7], got, c.want)
		}
	}

	if b, ok := CurrentBranch(repo); !ok || b != "master" {
		t.Errorf("CurrentBranch = %q, %v", b, ok)
	}
}
