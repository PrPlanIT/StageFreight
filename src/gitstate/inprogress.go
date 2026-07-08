package gitstate

import (
	"os"
	"path/filepath"
)

// DetectInProgressOp reports an in-progress git operation (merge, rebase, cherry-pick,
// revert) by probing the git directory for the marker files git itself writes. This is how
// StageFreight stays a first-class git citizen: it recognizes a state git created (mid-merge)
// and can refuse with guidance instead of silently flattening it.
//
// Best-effort: returns "" when the git directory can't be probed as a plain directory (e.g.
// a linked worktree whose .git is a file), which is no worse than the pre-detection behavior.
func DetectInProgressOp(rootDir string) string {
	gitDir := filepath.Join(rootDir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return ""
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("MERGE_HEAD"):
		return "merge"
	case exists("rebase-merge"), exists("rebase-apply"):
		return "rebase"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case exists("REVERT_HEAD"):
		return "revert"
	}
	return ""
}
