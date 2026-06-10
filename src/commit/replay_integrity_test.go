package commit

import (
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestReplayNestedPathsNotCorrupted reproduces the exact data-corruption scenario:
// a divergent upstream forces a replay of a commit that ADDS and MODIFIES nested
// files across two directories (src/gitstate/..., src/ci/...). It runs the real
// replayCommit and asserts the replayed commit's tree carries the full nested
// paths — never basenames at the repo root (the original bug wrote auth.go,
// transport.go, etc. to the root and produced an empty/garbage commit).
func TestReplayNestedPathsNotCorrupted(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	sig := &object.Signature{Name: "t", Email: "t@t"}

	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(content), 0o644); e != nil {
			t.Fatal(e)
		}
	}
	commit := func(msg string) plumbing.Hash {
		if e := wt.AddWithOptions(&git.AddOptions{All: true}); e != nil {
			t.Fatal(e)
		}
		h, e := wt.Commit(msg, &git.CommitOptions{Author: sig, AllowEmptyCommits: true})
		if e != nil {
			t.Fatal(e)
		}
		return h
	}

	write("README.md", "base\n")
	write("src/gitstate/existing.go", "package x // v1\n")
	base := commit("base")

	// local commit A on base: nested add + nested modify + a second-dir add
	write("src/gitstate/new.go", "package x // new\n")
	write("src/gitstate/existing.go", "package x // v2\n")
	write("src/ci/handoff.go", "package ci\n")
	aHash := commit("local A")

	// divergent upstream on base
	_ = wt.Reset(&git.ResetOptions{Commit: base, Mode: git.HardReset})
	write("other.txt", "upstream\n")
	uHash := commit("upstream U")

	// replay A onto U via the REAL replayCommit
	_ = wt.Reset(&git.ResetOptions{Commit: uHash, Mode: git.HardReset})
	aCommit, _ := repo.CommitObject(aHash)
	repoRoot := wt.Filesystem.Root()
	if err := replayCommit(repo, wt, repoRoot, aCommit, uHash); err != nil {
		t.Fatalf("replayCommit returned corruption/error: %v", err)
	}

	// no basename written at the repo root
	for _, b := range []string{"new.go", "existing.go", "handoff.go"} {
		if _, e := os.Stat(filepath.Join(repoRoot, b)); e == nil {
			t.Errorf("CORRUPTION: %q written at repo root", b)
		}
	}

	// the replayed HEAD commit's tree carries full nested paths
	head, _ := repo.Head()
	c, _ := repo.CommitObject(head.Hash())
	tree, _ := c.Tree()
	got := map[string]bool{}
	_ = tree.Files().ForEach(func(f *object.File) error { got[f.Name] = true; return nil })
	for _, p := range []string{
		"src/gitstate/new.go", "src/gitstate/existing.go", "src/ci/handoff.go",
		"other.txt", "README.md",
	} {
		if !got[p] {
			t.Errorf("replayed tree missing %q; got %v", p, sortedKeys(got))
		}
	}
}

// TestReplayCorruptionGuard locks the guard semantics: the original corruption
// (basename staged vs full-path source) and an empty-vs-nonempty mismatch are both
// rejected, while equal sets pass.
func TestReplayCorruptionGuard(t *testing.T) {
	full := map[string]bool{"src/gitstate/auth.go": true}
	base := map[string]bool{"auth.go": true}
	if equalStringSet(full, base) {
		t.Error("guard must reject basename staging vs full-path source (the corruption)")
	}
	if equalStringSet(map[string]bool{"x": true}, map[string]bool{}) {
		t.Error("guard must reject empty staging of a non-empty source")
	}
	if !equalStringSet(map[string]bool{"a": true, "b": true}, map[string]bool{"b": true, "a": true}) {
		t.Error("equal sets must match")
	}
}
