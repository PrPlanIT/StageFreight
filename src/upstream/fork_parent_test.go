package upstream

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestDefaultBranch reads the branch the remote's HEAD names, whatever it is called.
func TestDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	_, _ = wt.Add("f")
	c, err := wt.Commit("one", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("trunk"), c))
	_ = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("trunk")))

	got, err := DefaultBranch(context.Background(), Source{ID: "u", Kind: config.UpstreamKindGit, Location: dir})
	if err != nil || got != "trunk" {
		t.Errorf("default branch %q, %v", got, err)
	}
}

// TestUnresolvedForkParentFailsClosed: a fork_parent source whose parent is not known is
// never read from anywhere; every use reports why.
func TestUnresolvedForkParentFailsClosed(t *testing.T) {
	u := config.UpstreamConfig{Sources: config.OrderedUpstreamSources{
		{ID: "u", ForkParent: "github-mirror", ForkParentErr: "fork_parent github-mirror: the repository is not a fork on github"},
		{ID: "r", ForkParent: "github-mirror", ForkParentGit: "https://github.com/up/r.git"},
	}}
	srcs, err := Sources(u, "")
	if err != nil {
		t.Fatal(err)
	}
	if srcs[1].Location != "https://github.com/up/r.git" || srcs[1].Unresolved != "" || srcs[1].Kind != config.UpstreamKindGit {
		t.Errorf("a resolved fork parent is a plain git source: %+v", srcs[1])
	}
	un := srcs[0]
	ctx := context.Background()
	if _, err := List(ctx, un); err == nil || !strings.Contains(err.Error(), "not a fork") {
		t.Errorf("List: %v", err)
	}
	if _, err := BranchHead(ctx, un, "main"); err == nil {
		t.Error("BranchHead read an unresolved source")
	}
	if _, err := DefaultBranch(ctx, un); err == nil {
		t.Error("DefaultBranch read an unresolved source")
	}
	if err := Fetch(ctx, nil, un, plumbing.NewBranchReferenceName("main")); err == nil {
		t.Error("Fetch read an unresolved source")
	}
}
