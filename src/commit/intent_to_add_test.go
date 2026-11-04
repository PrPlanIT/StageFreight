package commit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
)

// intentToAddRepo returns a repo with one commit and new.txt marked with `git add -N`, plus
// a git runner for asserting what git itself reports.
func intentToAddRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "seed.txt")
	run("commit", "-q", "-m", "seed")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-N", "new.txt")
	return dir, run
}

// Staging a path that `git add -N` marked commits its content and leaves the index clean
// for git: the mark is cleared, as git's own add clears it.
func TestCommit_StagingClearsIntentToAdd(t *testing.T) {
	for _, mode := range []StageMode{StageExplicit, StageScoped, StageAll} {
		t.Run(string(mode), func(t *testing.T) {
			dir, run := intentToAddRepo(t)
			plan := &Plan{Type: "docs", Summary: "add new", StageMode: mode}
			if mode != StageAll {
				plan.Paths = []string{"new.txt"}
			}
			if _, err := (&GitBackend{RootDir: dir}).Execute(t.Context(), plan, true); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if got := run("show", "HEAD:new.txt"); got != "content\n" {
				t.Errorf("committed new.txt = %q, want its content", got)
			}
			if st := run("status", "--porcelain"); st != "" {
				t.Errorf("git status after the commit must be clean, got:\n%s", st)
			}
		})
	}
}

// git leaves a still-marked path out of a commit, but go-git would record it as an empty
// file. A marked path the commit did not stage refuses the commit instead.
func TestCommit_UnstagedIntentToAddRefuses(t *testing.T) {
	for _, mode := range []StageMode{StageExplicit, StageStaged} {
		t.Run(string(mode), func(t *testing.T) {
			dir, run := intentToAddRepo(t)
			if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run("add", "other.txt")
			plan := &Plan{Type: "docs", Summary: "add other", StageMode: mode}
			if mode == StageExplicit {
				plan.Paths = []string{"other.txt"}
			}
			head := run("rev-parse", "HEAD")

			_, err := (&GitBackend{RootDir: dir}).Execute(t.Context(), plan, true)
			if err == nil || !strings.Contains(err.Error(), "new.txt") || !strings.Contains(err.Error(), "intent-to-add") {
				t.Fatalf("commit must refuse, naming the marked path; got %v", err)
			}
			if got := run("rev-parse", "HEAD"); got != head {
				t.Error("a refused commit must not create a commit")
			}
			repo, err := git.PlainOpen(dir)
			if err != nil {
				t.Fatal(err)
			}
			idx, err := repo.Storer.Index()
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range idx.Entries {
				if e.Name == "new.txt" && !e.IntentToAdd {
					t.Error("the refusal must leave the operator's intent-to-add mark in place")
				}
			}
		})
	}
}
