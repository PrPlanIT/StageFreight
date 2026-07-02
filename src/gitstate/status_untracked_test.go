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
