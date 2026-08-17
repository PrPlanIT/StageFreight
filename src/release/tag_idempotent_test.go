package release

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func commitRepo(t *testing.T) (dir, sha string) {
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
	c, err := wt.Commit("c", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir, c.String()
}

// C — republish must leave an existing version tag untouched: a lightweight tag already at
// the right commit is NOT rewritten into an annotated one (which would change its object
// hash and reseed a mirror divergence).
func TestCreateAnnotatedTag_IdempotentLeavesExistingLightweight(t *testing.T) {
	dir, sha := commitRepo(t)
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateTag("v1", plumbing.NewHash(sha), nil); err != nil { // lightweight
		t.Fatal(err)
	}

	if err := CreateAnnotatedTag(dir, "v1", sha, "msg"); err != nil {
		t.Fatalf("idempotent create at same commit must not error: %v", err)
	}

	ref, err := repo.Reference(plumbing.NewTagReferenceName("v1"), false)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Hash().String() != sha {
		t.Errorf("tag was rewritten (hash %s != commit %s) — must leave the lightweight tag as-is", ref.Hash(), sha)
	}
}

// C — a version tag is an immutable identity: refusing to move it to a different commit.
func TestCreateAnnotatedTag_ConflictAtDifferentCommit(t *testing.T) {
	dir, sha := commitRepo(t)
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateTag("v1", plumbing.NewHash(sha), nil); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	_ = os.WriteFile(filepath.Join(dir, "g"), []byte("y"), 0o644)
	_, _ = wt.Add("g")
	c2, err := wt.Commit("c2", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateAnnotatedTag(dir, "v1", c2.String(), "msg"); err == nil {
		t.Error("moving an existing immutable version tag to a different commit must error")
	}
}

// C — when the version tag is absent it is cut ANNOTATED (the release convention): the
// resulting ref names a tag object, not the commit directly.
func TestCreateAnnotatedTag_CutsAnnotatedWhenAbsent(t *testing.T) {
	dir, sha := commitRepo(t)
	if err := CreateAnnotatedTag(dir, "v1", sha, "the release message"); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := repo.Reference(plumbing.NewTagReferenceName("v1"), false)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Hash().String() == sha {
		t.Error("expected an annotated tag (object hash != commit), got a lightweight tag")
	}
	if _, err := repo.TagObject(ref.Hash()); err != nil {
		t.Errorf("expected a tag object at the ref: %v", err)
	}
}
