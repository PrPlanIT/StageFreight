package gitstate

import (
	"testing"

	git "github.com/go-git/go-git/v5"
)

func TestIsCleanIgnoringUntracked(t *testing.T) {
	cases := []struct {
		name string
		s    git.Status
		want bool
	}{
		{"empty", git.Status{}, true},
		// go-git marks an untracked file with Staging: Untracked (NOT Unmodified) — this
		// is the exact shape wt.Status() emits; a wrong shape here masked a real bug.
		{"untracked only", git.Status{
			"scratch.md": &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked},
		}, true},
		{"staged", git.Status{
			"a.go": &git.FileStatus{Staging: git.Added, Worktree: git.Unmodified},
		}, false},
		{"unstaged tracked", git.Status{
			"b.go": &git.FileStatus{Staging: git.Unmodified, Worktree: git.Modified},
		}, false},
		{"untracked + staged", git.Status{
			"scratch.md": &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked},
			"a.go":       &git.FileStatus{Staging: git.Added, Worktree: git.Unmodified},
		}, false},
		{"untracked + unstaged tracked", git.Status{
			"scratch.md": &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked},
			"b.go":       &git.FileStatus{Staging: git.Unmodified, Worktree: git.Modified},
		}, false},
	}
	for _, c := range cases {
		if got := IsCleanIgnoringUntracked(c.s); got != c.want {
			t.Errorf("%s: IsCleanIgnoringUntracked = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStagedFilesExcludesUntracked(t *testing.T) {
	s := git.Status{
		"staged.txt":    &git.FileStatus{Staging: git.Added, Worktree: git.Unmodified},
		"tracked.txt":   &git.FileStatus{Staging: git.Unmodified, Worktree: git.Modified},
		"untracked.txt": &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked},
	}
	got := StagedFiles(s)
	if len(got) != 1 || got[0] != "staged.txt" {
		t.Errorf("StagedFiles = %v, want [staged.txt] (untracked and unstaged-tracked excluded)", got)
	}
	if HasStagedChanges(s) != true {
		t.Errorf("HasStagedChanges = false, want true (staged.txt is staged)")
	}
	// Untracked-only must report NO staged changes (else a nothing-to-commit check
	// would create a spurious empty commit).
	onlyUntracked := git.Status{
		"scratch.md": &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked},
	}
	if len(StagedFiles(onlyUntracked)) != 0 {
		t.Errorf("StagedFiles(untracked-only) = %v, want []", StagedFiles(onlyUntracked))
	}
	if HasStagedChanges(onlyUntracked) {
		t.Errorf("HasStagedChanges(untracked-only) = true, want false")
	}
}
