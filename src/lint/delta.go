package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/PrPlanIT/StageFreight/src/diag"
)

// Delta detects changed files relative to a baseline.
type Delta struct {
	RootDir      string
	TargetBranch string
	Verbose      bool
}

// ChangedFiles returns the list of files changed relative to the baseline.
// In CI mode, it diffs against STAGEFREIGHT_TARGET_BRANCH (or auto-detects).
// Locally, it diffs against HEAD (uncommitted + staged changes) plus
// committed changes not in the default branch.
// Returns nil (scan everything) if git is unavailable or no baseline exists.
func (d *Delta) ChangedFiles(ctx context.Context) (map[string]bool, error) {
	repo, err := git.PlainOpen(d.RootDir)
	if err != nil {
		diag.Debug(d.Verbose, "delta: not a git repo, scanning all files")
		return nil, nil // not a git repo — full scan
	}

	// Collect changed paths from working tree (unstaged + staged)
	worktreeChanges, err := d.worktreeChanges(repo)
	if err != nil {
		diag.Debug(d.Verbose, "delta: worktree diff failed: %v, scanning all files", err)
		return nil, nil
	}

	// Collect changed paths relative to target branch
	branchChanges, err := d.branchChanges(repo)
	if err != nil {
		diag.Debug(d.Verbose, "delta: branch diff failed: %v, scanning all files", err)
		return nil, nil
	}

	// Merge both sets
	changed := make(map[string]bool)
	for p := range worktreeChanges {
		changed[p] = true
	}
	for p := range branchChanges {
		changed[p] = true
	}

	if len(changed) == 0 {
		diag.Debug(d.Verbose, "delta: no changes detected")
	}

	return changed, nil
}

// worktreeChanges returns files with uncommitted modifications (staged + unstaged).
func (d *Delta) worktreeChanges(repo *git.Repository) (map[string]bool, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	changed := make(map[string]bool)
	for path, s := range status {
		if s.Worktree == git.Unmodified && s.Staging == git.Unmodified {
			continue
		}
		changed[path] = true
	}

	return changed, nil
}

// branchChanges returns files changed between HEAD and the target branch.
func (d *Delta) branchChanges(repo *git.Repository) (map[string]bool, error) {
	targetBranch := d.targetBranch()
	if targetBranch == "" {
		return nil, nil // no target branch — skip branch diff
	}

	headRef, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("getting HEAD: %w", err)
	}

	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("getting HEAD commit: %w", err)
	}

	// Resolve target branch
	targetRef, err := repo.Reference(plumbing.NewBranchReferenceName(targetBranch), true)
	if err != nil {
		// Try remote reference
		targetRef, err = repo.Reference(plumbing.NewRemoteReferenceName("origin", targetBranch), true)
		if err != nil {
			return nil, nil // target branch not found — skip
		}
	}

	targetCommit, err := repo.CommitObject(targetRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("getting target commit: %w", err)
	}

	// If HEAD is the same as target (e.g., push to main), fall back to
	// diffing HEAD vs its parent so the latest commit's changes get linted.
	if headCommit.Hash == targetCommit.Hash {
		if headCommit.NumParents() == 0 {
			return nil, nil // initial commit — nothing to diff
		}
		parent, err := headCommit.Parent(0)
		if err != nil {
			return nil, nil
		}
		targetCommit = parent
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}
	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTreeWithOptions(context.Background(), targetTree, headTree, &object.DiffTreeOptions{})
	if err != nil {
		return nil, fmt.Errorf("diffing trees: %w", err)
	}

	changed := make(map[string]bool)
	for _, change := range changes {
		name := changeName(change)
		if name != "" {
			changed[name] = true
		}
	}

	return changed, nil
}

// targetBranch determines the branch to diff against.
func (d *Delta) targetBranch() string {
	// 1. Explicit env var takes priority
	if branch := os.Getenv("STAGEFREIGHT_TARGET_BRANCH"); branch != "" {
		return branch
	}

	// 2. Config-level override
	if d.TargetBranch != "" {
		return d.TargetBranch
	}

	// 3. Common CI env vars
	ciVars := []string{
		"CI_MERGE_REQUEST_TARGET_BRANCH_NAME", // GitLab CI
		"GITHUB_BASE_REF",                     // GitHub Actions
		"BITBUCKET_PR_DESTINATION_BRANCH",     // Bitbucket
		"CHANGE_TARGET",                       // Jenkins
	}
	for _, v := range ciVars {
		if branch := os.Getenv(v); branch != "" {
			return branch
		}
	}

	// 4. Detect from git remote HEAD
	if branch := d.detectDefaultBranch(); branch != "" {
		return branch
	}

	// 5. Fallback
	return "main"
}

// detectDefaultBranch reads the symbolic ref for origin/HEAD to determine
// the remote's default branch.
func (d *Delta) detectDefaultBranch() string {
	repo, err := git.PlainOpen(d.RootDir)
	if err != nil {
		return ""
	}
	// Don't resolve (false) — we need the symbolic ref target, not the commit hash
	ref, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", "HEAD"), false)
	if err != nil {
		return ""
	}
	// ref.Target() is like "refs/remotes/origin/main"
	target := ref.Target().String()
	prefix := "refs/remotes/origin/"
	if strings.HasPrefix(target, prefix) {
		return strings.TrimPrefix(target, prefix)
	}
	return ""
}

// changeName extracts the file path from a tree change.
func changeName(change *object.Change) string {
	action, err := change.Action()
	if err != nil {
		return ""
	}
	switch action {
	case merkletrie.Insert, merkletrie.Modify:
		return change.To.Name
	case merkletrie.Delete:
		return change.From.Name
	}
	return ""
}

// FilterByDelta filters a file list to only include changed files.
// If changedSet is nil, returns all files (full scan).
func FilterByDelta(files []FileInfo, changedSet map[string]bool) []FileInfo {
	if changedSet == nil {
		return files
	}

	filtered := make([]FileInfo, 0, len(changedSet))
	for _, f := range files {
		path := filepath.ToSlash(f.Path)
		if changedSet[path] || changedSet[strings.TrimPrefix(path, "./")] {
			filtered = append(filtered, f)
		}
	}
	return filtered
}
