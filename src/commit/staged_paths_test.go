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

// A commit must never silently publish references to content it failed to stage.
// The real-world shape: a repo whose ROOT .gitignore excludes .stagefreight/, which
// voids StageFreight's own namespace allowlist (git cannot re-include a path whose
// parent directory is ignored). Scribe declares [README.md, .stagefreight/scribe],
// git drops the SVGs, and the commit "succeeds" with a README full of 404 badges.
func TestCommit_DeclaredPathDroppedByGitignoreFailsLoudly(t *testing.T) {
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
	// The shadowing root ignore, plus the content scribe would publish.
	write(".gitignore", ".stagefreight/\n")
	write("README.md", "![license](.stagefreight/scribe/license.svg)\n")
	write(".stagefreight/scribe/license.svg", "<svg/>")

	// Seed a commit so the repo has a HEAD.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(".gitignore"); err != nil {
		t.Fatal(err)
	}

	backend := &GitBackend{RootDir: dir}
	_, err = backend.Execute(t.Context(), &Plan{
		Type: "docs", Summary: "refresh generated badges",
		StageMode: StageExplicit,
		Paths:     []string{"README.md", ".stagefreight/scribe"},
	}, true)

	if err == nil {
		t.Fatal("commit must FAIL when a declared path was dropped by staging — " +
			"silently committing the README alone publishes broken references")
	}
	msg := err.Error()
	if !strings.Contains(msg, ".stagefreight/scribe") || !strings.Contains(msg, "gitignore") {
		t.Errorf("error must name the dropped path and the cause, got: %v", err)
	}
}

// The check must not fire on the normal case: declared paths that are already tracked
// and unchanged contribute nothing to stage, and that is perfectly valid.
func TestCommit_DeclaredPathTrackedUnchangedIsFine(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "README.md")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}

	backend := &GitBackend{RootDir: dir}
	if _, err := backend.Execute(t.Context(), &Plan{
		Type: "docs", Summary: "no-op",
		StageMode: StageExplicit,
		Paths:     []string{"README.md"},
	}, true); err != nil {
		t.Fatalf("tracked+unchanged declared path must not be reported as dropped, got %v", err)
	}
}
