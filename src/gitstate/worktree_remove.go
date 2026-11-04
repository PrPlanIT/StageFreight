package gitstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// WorktreeState is what a worktree holds that git's own listing doesn't say: how it stands
// against its upstream, what's uncommitted, and whether its work exists anywhere else.
type WorktreeState struct {
	Head       plumbing.Hash
	Upstream   string // e.g. origin/main; empty when the branch tracks nothing
	Ahead      int
	Behind     int
	Changes    int  // files with uncommitted changes, untracked included
	Elsewhere  bool // HEAD is contained in some other branch (local or remote)
	MergedInto string
	Merged     bool
}

// InspectWorktree reads a worktree's state. defaultBranch is the branch merged-ness is
// measured against; empty skips it.
func InspectWorktree(w Worktree, defaultBranch string) (WorktreeState, error) {
	var s WorktreeState
	repo, err := OpenRepo(w.Path)
	if err != nil {
		return s, err
	}
	head, err := repo.Head()
	if err != nil {
		return s, err
	}
	s.Head = head.Hash()

	wt, err := repo.Worktree()
	if err != nil {
		return s, err
	}
	status, err := wt.Status()
	if err != nil {
		return s, err
	}
	for _, fs := range status {
		if fs.Staging != git.Unmodified || fs.Worktree != git.Unmodified {
			s.Changes++
		}
	}

	if w.Branch != "" {
		// An upstream whose remote branch is gone counts as none, so the commits it held
		// are judged by whether any other branch has them.
		if up, ok := UpstreamBranchRef(repo, w.Branch); ok {
			if ref, err := repo.Reference(up, true); err == nil {
				s.Upstream = up.Short()
				s.Ahead, s.Behind, _ = countAheadBehind(repo, s.Head, ref.Hash())
			}
		}
	}
	s.Elsewhere, err = containedElsewhere(repo, s.Head, w.Branch)
	if err != nil {
		return s, err
	}
	if defaultBranch != "" && w.Branch != "" && w.Branch != defaultBranch {
		s.MergedInto = defaultBranch
		if ref, err := repo.Reference(plumbing.NewBranchReferenceName(defaultBranch), true); err == nil {
			s.Merged, _ = isAncestorHash(repo, s.Head, ref.Hash())
		}
	}
	return s, nil
}

// UpstreamBranchRef returns the remote-tracking ref a local branch tracks.
func UpstreamBranchRef(repo *git.Repository, branch string) (plumbing.ReferenceName, bool) {
	cfg, err := repo.Config()
	if err != nil {
		return "", false
	}
	b := cfg.Branches[branch]
	if b == nil || b.Remote == "" || b.Merge == "" || !b.Merge.IsBranch() {
		return "", false
	}
	return plumbing.NewRemoteReferenceName(b.Remote, b.Merge.Short()), true
}

// containedElsewhere reports whether commit is on any branch other than own, local or
// remote-tracking.
func containedElsewhere(repo *git.Repository, commit plumbing.Hash, own string) (bool, error) {
	refs, err := repo.References()
	if err != nil {
		return false, err
	}
	found := false
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if found || ref.Type() != plumbing.HashReference {
			return nil
		}
		n := ref.Name()
		if !(n.IsBranch() && n.Short() != own) && !(n.IsRemote() && !strings.HasSuffix(n.String(), "/HEAD")) {
			return nil
		}
		ok, err := isAncestorHash(repo, commit, ref.Hash())
		if err != nil {
			return err
		}
		found = ok
		return nil
	})
	return found, err
}

// WorktreeRefusal is why remove declined: what removing the worktree would lose.
type WorktreeRefusal struct {
	Path    string
	Reasons []string
}

func (r *WorktreeRefusal) Error() string {
	return fmt.Sprintf("worktree remove: %s holds work that would be lost:\n  - %s\nre-run with --force to remove it anyway",
		r.Path, strings.Join(r.Reasons, "\n  - "))
}

// FindWorktree resolves `remove`'s argument — a worktree's path, or its id — as git does.
func FindWorktree(dir, target string) (Worktree, error) {
	wts, err := Worktrees(dir)
	if err != nil {
		return Worktree{}, err
	}
	abs, _ := filepath.Abs(target)
	for _, w := range wts {
		if w.Path == abs || (!w.Main && w.ID == target) {
			return w, nil
		}
	}
	return Worktree{}, fmt.Errorf("worktree remove: %q is not a working tree", target)
}

// RemoveWorktree deletes a linked worktree and its administrative files, keeping its
// branch, as `git worktree remove` does. Like git it refuses the main worktree, a locked
// one, and uncommitted changes without force; it also refuses, without force, commits that
// exist nowhere else — ahead of the upstream, or on no other branch.
func RemoveWorktree(dir, target string, force bool) (Worktree, error) {
	w, err := FindWorktree(dir, target)
	if err != nil {
		return w, err
	}
	switch {
	case w.Main:
		return w, fmt.Errorf("worktree remove: %s is the main worktree", w.Path)
	case w.Locked:
		return w, fmt.Errorf("worktree remove: %s is locked; unlock it first", w.Path)
	}
	if !w.Prunable && !force {
		s, err := InspectWorktree(w, "")
		if err != nil {
			return w, fmt.Errorf("worktree remove: can't read %s, so can't tell what removing it loses: %w; --force removes it anyway", w.Path, err)
		}
		var reasons []string
		if s.Changes > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) with uncommitted changes", s.Changes))
		}
		switch {
		case s.Upstream != "" && s.Ahead > 0:
			reasons = append(reasons, fmt.Sprintf("%d commit(s) %s doesn't have", s.Ahead, s.Upstream))
		case s.Upstream == "" && !s.Elsewhere:
			what := "branch " + w.Branch
			if w.Branch == "" {
				what = "detached HEAD " + s.Head.String()[:8]
			}
			reasons = append(reasons, fmt.Sprintf("%s has commits no other branch has", what))
		}
		if len(reasons) > 0 {
			return w, &WorktreeRefusal{Path: w.Path, Reasons: reasons}
		}
	}
	if !w.Prunable {
		if err := os.RemoveAll(w.Path); err != nil {
			return w, err
		}
	}
	if err := os.RemoveAll(w.adminDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return w, err
	}
	return w, nil
}
