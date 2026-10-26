package commit

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// committedFiles lists the paths in HEAD's tree.
func committedFiles(t *testing.T, repo *git.Repository) []string {
	t.Helper()
	ref, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	_ = tree.Files().ForEach(func(f *object.File) error { out = append(out, f.Name); return nil })
	sort.Strings(out)
	return out
}

// TestCommit_NamespaceIgnorePlantedOnlyWhenManaged: a repo StageFreight manages gets its
// namespace ignore on the first commit; a repo it does not manage gets exactly the paths
// committed, nothing of StageFreight's.
func TestCommit_NamespaceIgnorePlantedOnlyWhenManaged(t *testing.T) {
	for _, managed := range []bool{false, true} {
		dir := t.TempDir()
		repo, err := git.PlainInit(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{"README.md"}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if managed {
			if err := os.WriteFile(filepath.Join(dir, ".stagefreight.yml"), []byte("version: 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, ".stagefreight.yml")
		}
		backend := &GitBackend{RootDir: dir}
		if _, err := backend.Execute(t.Context(), &Plan{Type: "docs", Summary: "readme", StageMode: StageExplicit, Paths: paths}, true); err != nil {
			t.Fatalf("managed=%v: %v", managed, err)
		}
		got := committedFiles(t, repo)
		_, planted := os.Stat(filepath.Join(dir, ".stagefreight", ".gitignore"))
		switch {
		case managed && (len(got) != 3 || got[0] != ".stagefreight.yml" || got[1] != ".stagefreight/.gitignore"):
			t.Errorf("managed repo must commit the namespace ignore too, got %v", got)
		case !managed && (len(got) != 1 || got[0] != "README.md" || planted == nil):
			t.Errorf("unmanaged repo must get only README.md and no planted file, got %v (planted: %v)", got, planted == nil)
		}
	}
}
