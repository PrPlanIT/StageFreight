package commit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// defaultTestRegistry accepts the plain conventional types these tests commit with.
func defaultTestRegistry(t *testing.T) *TypeRegistry {
	t.Helper()
	return NewTypeRegistry([]config.CommitType{
		{Key: "feat"}, {Key: "fix"}, {Key: "chore"}, {Key: "docs"},
	})
}

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

// Naming a file that was deleted from the worktree commits the deletion. `git commit --
// <path>` accepts this, and refusing it makes `-- paths` unable to express a removal.
func TestCommit_ScopedCommitsADeletion(t *testing.T) {
	dir, repo, write := seedRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write("doomed.txt", "doomed\n")
	if _, err := wt.Add("doomed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add doomed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "doomed.txt")); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(PlannerOptions{
		Type: "chore", Message: "drop it", Paths: []string{"doomed.txt"},
	}, config.CommitConfig{}, defaultTestRegistry(t), dir)
	if err != nil {
		t.Fatalf("planning a deletion was rejected: %v", err)
	}

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), plan, true)
	if err != nil {
		t.Fatalf("committing a deletion failed: %v", err)
	}
	if !contains(res.Files, "doomed.txt") {
		t.Errorf("committed %v, want the deletion of doomed.txt", res.Files)
	}
}

// A rename stages the source as a deletion, which removes it from the index. Naming both
// sides is how the rename gets committed, so the source must not read as a bad path.
func TestCommit_ScopedCommitsARename(t *testing.T) {
	dir, repo, write := seedRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write("old/name.txt", "content\n")
	if _, err := wt.Add("old/name.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add original", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	// Perform the rename the way git does: the removal is already staged.
	if err := os.Remove(filepath.Join(dir, "old/name.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("old/name.txt"); err != nil {
		t.Fatal(err)
	}
	write("new/name.txt", "content\n")

	plan, err := BuildPlan(PlannerOptions{
		Type: "chore", Message: "move it", Paths: []string{"old/name.txt", "new/name.txt"},
	}, config.CommitConfig{}, defaultTestRegistry(t), dir)
	if err != nil {
		t.Fatalf("planning a rename was rejected: %v", err)
	}

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), plan, true)
	if err != nil {
		t.Fatalf("committing a rename failed: %v", err)
	}
	if !contains(res.Files, "old/name.txt") || !contains(res.Files, "new/name.txt") {
		t.Errorf("committed %v, want both sides of the rename", res.Files)
	}
}

// A path git has never heard of contributes nothing, so it is still refused — the fix is
// for absent-but-tracked paths, not for typos.
func TestCommit_ScopedRejectsUnknownPath(t *testing.T) {
	dir, _, _ := seedRepo(t)

	_, err := BuildPlan(PlannerOptions{
		Type: "chore", Message: "nope", Paths: []string{"never/existed.txt"},
	}, config.CommitConfig{}, defaultTestRegistry(t), dir)
	if err == nil {
		t.Fatal("a path neither present nor tracked was accepted")
	}
	if !strings.Contains(err.Error(), "never/existed.txt") {
		t.Errorf("error must name the offending path, got: %v", err)
	}
}

// Naming a deleted directory must commit the removal of everything that was under it.
// go-git's Add matches index entries by exact name and a directory is never an entry, so
// without help the commit succeeds having staged nothing — the silent drop this package
// exists to prevent.
func TestCommit_ScopedCommitsADeletedDirectory(t *testing.T) {
	dir, repo, write := seedRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write("pkg/a.txt", "a\n")
	write("pkg/nested/b.txt", "b\n")
	if _, err := wt.Add("pkg"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add pkg", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "pkg")); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(PlannerOptions{
		Type: "chore", Message: "drop the package", Paths: []string{"pkg"},
	}, config.CommitConfig{}, defaultTestRegistry(t), dir)
	if err != nil {
		t.Fatalf("planning a deleted directory was rejected: %v", err)
	}

	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), plan, true)
	if err != nil {
		t.Fatalf("committing a deleted directory failed: %v", err)
	}
	for _, want := range []string{"pkg/a.txt", "pkg/nested/b.txt"} {
		if !contains(res.Files, want) {
			t.Errorf("committed %v, want it to include the deletion of %s", res.Files, want)
		}
	}

	// And the tree must actually be free of them afterwards.
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.File("pkg/a.txt"); err == nil {
		t.Error("pkg/a.txt is still in the committed tree")
	}
}
