package commit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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

// filesAt lists the paths in a commit's tree.
func filesAt(t *testing.T, repo *git.Repository, c *object.Commit) []string {
	t.Helper()
	tree, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	_ = tree.Files().ForEach(func(f *object.File) error { out = append(out, f.Name); return nil })
	sort.Strings(out)
	return out
}

// TestCommit_NamespaceIgnoreCommittedSeparately: a scoped commit holds exactly the paths
// it named; the namespace ignore follows in a commit of its own, and only when it
// differs from HEAD.
func TestCommit_NamespaceIgnoreCommittedSeparately(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"README.md": "hi\n", ".stagefreight.yml": "version: 1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	backend := &GitBackend{RootDir: dir}
	res, err := backend.Execute(t.Context(), &Plan{Type: "docs", Summary: "readme", StageMode: StageScoped,
		Paths: []string{"README.md", ".stagefreight.yml"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA == "" || res.NamespaceIgnoreSHA == "" {
		t.Fatalf("want the requested commit and a namespace commit, got sha=%q namespace=%q", res.SHA, res.NamespaceIgnoreSHA)
	}
	requested, err := repo.CommitObject(plumbing.NewHash(res.SHA))
	if err != nil {
		t.Fatal(err)
	}
	if got := filesAt(t, repo, requested); len(got) != 2 || got[0] != ".stagefreight.yml" || got[1] != "README.md" {
		t.Errorf("requested commit must hold only its named paths, got %v", got)
	}
	ns, err := repo.CommitObject(plumbing.NewHash(res.NamespaceIgnoreSHA))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ns.Message, namespaceIgnoreMessage) {
		t.Errorf("namespace commit message = %q", ns.Message)
	}
	if len(ns.ParentHashes) != 1 || ns.ParentHashes[0] != requested.Hash {
		t.Errorf("namespace commit must follow the requested one")
	}
	if got := committedFiles(t, repo); len(got) != 3 || got[1] != ".stagefreight/.gitignore" {
		t.Errorf("HEAD must carry the namespace ignore, got %v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = backend.Execute(t.Context(), &Plan{Type: "docs", Summary: "readme again", StageMode: StageScoped,
		Paths: []string{"README.md"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.NamespaceIgnoreSHA != "" {
		t.Errorf("an unchanged namespace ignore needs no commit, got %q", res.NamespaceIgnoreSHA)
	}
}
