package lint

import (
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Baseline is the prior state a run is diffed against. Comparison semantics
// (Paths/Content) are kept separate from the source on purpose: only a git merge-base
// resolver is wired today, but the type means "baseline" is not welded to git refs — a
// future resolver (a stored snapshot, a tarball, an attestation) could back the same two
// queries without changing any diff logic. One resolver now; clean seam for later.
type Baseline struct {
	tree   *object.Tree
	Commit string // short-ish id of the resolved baseline commit, for display
}

// ResolveBaseline finds the merge-base of HEAD and the target branch and returns its tree
// (or HEAD's parent when HEAD == target, e.g. a push to main). It is intentionally
// forgiving: no git repo, no target branch, no common ancestor → (nil, false, nil), so a
// caller degrades to "no baseline" and never fails a lint run over git topology.
func ResolveBaseline(rootDir, targetBranch string) (*Baseline, bool, error) {
	repo, err := git.PlainOpen(rootDir)
	if err != nil {
		return nil, false, nil
	}
	head, err := repo.Head()
	if err != nil {
		return nil, false, nil
	}
	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, false, nil
	}
	base := resolveBaseCommit(repo, headCommit, targetBranch)
	if base == nil {
		return nil, false, nil
	}
	tree, err := base.Tree()
	if err != nil {
		return nil, false, nil
	}
	short := base.Hash.String()
	if len(short) > 12 {
		short = short[:12]
	}
	return &Baseline{tree: tree, Commit: short}, true, nil
}

// resolveBaseCommit picks the commit to diff against: the merge-base of HEAD and the
// target branch; or HEAD's parent when HEAD already is the target tip (push-to-main); or
// the target tip if no common ancestor is found.
func resolveBaseCommit(repo *git.Repository, head *object.Commit, targetBranch string) *object.Commit {
	if targetBranch == "" {
		targetBranch = "main"
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(targetBranch), true)
	if err != nil {
		ref, err = repo.Reference(plumbing.NewRemoteReferenceName("origin", targetBranch), true)
		if err != nil {
			return firstParent(head) // no target branch — compare against the previous commit
		}
	}
	target, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil
	}
	if head.Hash == target.Hash {
		return firstParent(head)
	}
	bases, err := head.MergeBase(target)
	if err != nil || len(bases) == 0 {
		return target // diverged with no common ancestor — fall back to the target tip
	}
	return bases[0]
}

func firstParent(c *object.Commit) *object.Commit {
	if c.NumParents() == 0 {
		return nil
	}
	p, err := c.Parent(0)
	if err != nil {
		return nil
	}
	return p
}

// Paths returns the set of file paths present in the baseline tree. Used by the
// new-artifact diff (Slice A): a path absent here but present now is newly introduced —
// a clean, content-free, unambiguous signal.
func (b *Baseline) Paths() (map[string]bool, error) {
	set := map[string]bool{}
	files := b.tree.Files()
	defer files.Close()
	err := files.ForEach(func(f *object.File) error {
		set[f.Name] = true
		return nil
	})
	return set, err
}

// Content returns the baseline bytes of a path; ok=false if the path did not exist in the
// baseline. Used by the finding-level diff (Slice B) to obtain base findings.
func (b *Baseline) Content(path string) ([]byte, bool, error) {
	f, err := b.tree.File(path)
	if err == object.ErrFileNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s, err := f.Contents()
	if err != nil {
		return nil, false, err
	}
	return []byte(s), true, nil
}
