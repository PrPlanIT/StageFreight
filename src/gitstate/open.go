package gitstate

import (
	"fmt"

	git "github.com/go-git/go-git/v5"
)

// OpenRepo is the single entry point for all *git.Repository instances.
// No package outside src/gitstate/ or src/commit/ may call git.PlainOpen directly.
// DetectDotGit walks parent directories to find .git, matching git CLI behaviour.
// EnableDotGitCommonDir resolves a LINKED WORKTREE's `.git` file
// (gitdir: …/.git/worktrees/<name>) against the shared common dir, so objects and refs are
// read/written to the real store — not the worktree's private dir. Without it a commit made
// in a worktree is silently MISPLACED (a hash is returned but the object never enters the
// shared store and the branch is left dangling). Harmless for a normal repo, whose common
// dir IS its git dir.
func OpenRepo(rootDir string) (*git.Repository, error) {
	return git.PlainOpenWithOptions(rootDir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}

// RepoRoot returns the absolute path of the repository root directory (the
// directory containing .git). Use this instead of wt.Filesystem.Root() directly
// — encapsulates the go-git worktree filesystem contract in one place.
func RepoRoot(repo *git.Repository) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("opening worktree: %w", err)
	}
	return wt.Filesystem.Root(), nil
}
