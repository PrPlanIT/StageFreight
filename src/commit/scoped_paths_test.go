package commit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// seedRepo returns a repo with one commit so HEAD exists, plus a writer for new files.
func seedRepo(t *testing.T) (string, *git.Repository, func(rel, content string)) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write("seed.txt", "seed\n")
	if _, err := wt.Add("seed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	return dir, repo, write
}

// A commit that names its paths must not also publish whatever someone else left in the
// index — a colleague mid-change, another tool, an earlier aborted run.
func TestCommit_ScopedRefusesUndeclaredStagedPath(t *testing.T) {
	dir, repo, write := seedRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	write("mine.txt", "mine\n")
	write("theirs.txt", "theirs\n")
	if _, err := wt.Add("theirs.txt"); err != nil {
		t.Fatal(err)
	}

	backend := &GitBackend{RootDir: dir}
	_, err = backend.Execute(t.Context(), &Plan{
		Type: "feat", Summary: "only mine",
		StageMode: StageScoped,
		Paths:     []string{"mine.txt"},
	}, true)

	if err == nil {
		t.Fatal("scoped commit accepted a staged path it never named")
	}
	if !strings.Contains(err.Error(), "theirs.txt") {
		t.Errorf("error must name the undeclared path, got: %v", err)
	}
}

// The bound is what the commit contains, so with nothing else staged it commits exactly
// the declared set.
func TestCommit_ScopedCommitsOnlyDeclared(t *testing.T) {
	dir, _, write := seedRepo(t)
	write("mine.txt", "mine\n")
	write("untouched.txt", "untouched\n")

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), &Plan{
		Type: "feat", Summary: "only mine",
		StageMode: StageScoped,
		Paths:     []string{"mine.txt"},
	}, true)
	if err != nil {
		t.Fatalf("scoped commit failed: %v", err)
	}
	if !contains(res.Files, "mine.txt") {
		t.Errorf("committed %v, want it to include mine.txt", res.Files)
	}
	if contains(res.Files, "untouched.txt") {
		t.Errorf("committed %v, which includes a path the commit never named", res.Files)
	}
}

// A declared directory covers what is beneath it.
func TestCommit_ScopedDirectoryCoversChildren(t *testing.T) {
	dir, _, write := seedRepo(t)
	write("pkg/a.txt", "a\n")
	write("pkg/nested/b.txt", "b\n")

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), &Plan{
		Type: "feat", Summary: "the whole package",
		StageMode: StageScoped,
		Paths:     []string{"pkg"},
	}, true)
	if err != nil {
		t.Fatalf("declared directory rejected its own children: %v", err)
	}
	if !contains(res.Files, "pkg/a.txt") || !contains(res.Files, "pkg/nested/b.txt") {
		t.Errorf("committed %v, want both files under pkg/", res.Files)
	}
}

// --add is the additive surface and keeps sweeping the index; that is its contract, and
// it is what a caller reaches for when they mean "these as well".
func TestCommit_AddModeStillIncludesPreStaged(t *testing.T) {
	dir, repo, write := seedRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write("mine.txt", "mine\n")
	write("already.txt", "already\n")
	if _, err := wt.Add("already.txt"); err != nil {
		t.Fatal(err)
	}

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), &Plan{
		Type: "feat", Summary: "mine plus what was staged",
		StageMode: StageExplicit,
		Paths:     []string{"mine.txt"},
	}, true)
	if err != nil {
		t.Fatalf("additive commit failed: %v", err)
	}
	if !contains(res.Files, "mine.txt") || !contains(res.Files, "already.txt") {
		t.Errorf("committed %v, want the declared path and the pre-staged one", res.Files)
	}
}

func TestAssertNoUndeclaredStaged(t *testing.T) {
	for _, c := range []struct {
		name     string
		declared []string
		staged   []string
		wantErr  bool
	}{
		{"exact match", []string{"a.txt"}, []string{"a.txt"}, false},
		{"directory covers child", []string{"pkg"}, []string{"pkg/a.txt"}, false},
		{"nested child", []string{"pkg"}, []string{"pkg/deep/a.txt"}, false},
		{"undeclared sibling", []string{"a.txt"}, []string{"a.txt", "b.txt"}, true},
		{"prefix is not a parent", []string{"pkg"}, []string{"pkgother/a.txt"}, true},
		{"nothing staged", []string{"a.txt"}, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := assertNoUndeclaredStaged(c.declared, c.staged)
			if c.wantErr && err == nil {
				t.Errorf("declared=%v staged=%v: expected refusal", c.declared, c.staged)
			}
			if !c.wantErr && err != nil {
				t.Errorf("declared=%v staged=%v: unexpected refusal: %v", c.declared, c.staged, err)
			}
		})
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
