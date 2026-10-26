package build

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// A linked worktree's .git is a file, not a directory; reading .git/config off disk found
// no origin there, and `release create` failed with "no git remote URL found".
func TestDetectGitInfo_LinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to create a linked worktree")
	}
	tmp := t.TempDir()
	main := filepath.Join(tmp, "main")
	wt := filepath.Join(tmp, "wt")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", main},
		{"-C", main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base"},
		{"-C", main, "remote", "add", "origin", "https://example.com/org/repo.git"},
		{"-C", main, "worktree", "add", "-q", "-b", "feature", wt},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	info := detectGitInfo(wt)
	if info == nil || info.Remote != "https://example.com/org/repo.git" || info.Branch != "feature" {
		t.Fatalf("worktree git info = %+v, want origin https://example.com/org/repo.git on feature", info)
	}
}
