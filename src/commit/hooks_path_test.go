package commit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
)

const rejectCoAuthor = "#!/bin/sh\nif grep -qi '^Co-Authored-By: Claude' \"$1\"; then echo 'no Claude co-author trailer' >&2; exit 1; fi\n"

func writeHook(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commit-msg"), []byte(rejectCoAuthor), 0o755); err != nil {
		t.Fatal(err)
	}
}

func isolateGitConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func assertRejects(t *testing.T, root string) {
	t.Helper()
	_, err := RunCommitMsgHook(root, "fix: x\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n", nil)
	var rej *gitstate.ErrHookRejected
	if !errors.As(err, &rej) {
		t.Fatalf("the commit-msg hook did not run: err=%v", err)
	}
	if msg, err := RunCommitMsgHook(root, "fix: x\n", nil); err != nil || msg != "fix: x\n" {
		t.Fatalf("a clean message passes: msg=%q err=%v", msg, err)
	}
}

func TestCommitMsgHookHonoursRepoHooksPath(t *testing.T) {
	isolateGitConfig(t)
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	writeHook(t, filepath.Join(root, ".githooks"))
	f, _ := os.OpenFile(filepath.Join(root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("[core]\n\thooksPath = .githooks\n")
	f.Close()
	assertRejects(t, root)
}

func TestCommitMsgHookHonoursGlobalHooksPath(t *testing.T) {
	home := isolateGitConfig(t)
	writeHook(t, filepath.Join(home, "hooks"))
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\thooksPath = ~/hooks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	assertRejects(t, root)
}

// In a linked worktree .git is a file; hooks live in the shared git directory.
func TestCommitMsgHookRunsInALinkedWorktree(t *testing.T) {
	isolateGitConfig(t)
	main := t.TempDir()
	if _, err := git.PlainInit(main, false); err != nil {
		t.Fatal(err)
	}
	writeHook(t, filepath.Join(main, ".git", "hooks"))
	wtGitDir := filepath.Join(main, ".git", "worktrees", "feature")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644)
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644)
	assertRejects(t, wt)
}
