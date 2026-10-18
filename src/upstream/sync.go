package upstream

import (
	"context"
	"errors"
	"fmt"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream/merge"
)

// Sync outcomes.
const (
	SyncInSync   = "in-sync"  // upstream's commit is already in our history
	SyncSynced   = "synced"   // NewHead joins upstream's commit with ours
	SyncConflict = "conflict" // upstream's changes overlap ours; nothing was produced
)

// SyncPlan is what a sync would do to our branch. It only writes git objects; moving any ref
// or pushing is the caller's decision.
type SyncPlan struct {
	Result    string
	NewHead   plumbing.Hash
	Rewrites  bool     // NewHead does not descend from our head (a rebase): pushing it is a force push
	Conflicts []string // paths (and, for a rebase, the commit) that could not be joined
	Reason    string
}

// PlanSync joins upstream's commit target into our head with the strategy. sig signs a merge
// commit; message is its message.
func PlanSync(repo *git.Repository, strategy string, head, target plumbing.Hash, sig object.Signature, message string) (SyncPlan, error) {
	headC, err := repo.CommitObject(head)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("our head %s: %w", head, err)
	}
	targetC, err := repo.CommitObject(target)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("upstream commit %s: %w", target, err)
	}
	if inHistory, err := targetC.IsAncestor(headC); err != nil {
		return SyncPlan{}, err
	} else if inHistory || head == target {
		return SyncPlan{Result: SyncInSync, NewHead: head}, nil
	}
	// Nothing of ours on top of upstream's line: every strategy is a fast-forward.
	if ff, err := headC.IsAncestor(targetC); err != nil {
		return SyncPlan{}, err
	} else if ff {
		return SyncPlan{Result: SyncSynced, NewHead: target}, nil
	}
	if strategy == config.SyncMirror {
		return SyncPlan{Result: SyncConflict, Reason: "our branch has diverged from upstream's; a mirror only fast-forwards, so it is kept as it is"}, nil
	}
	bases, err := headC.MergeBase(targetC)
	if err != nil {
		return SyncPlan{}, err
	}
	if len(bases) == 0 {
		return SyncPlan{Result: SyncConflict, Reason: "our history shares no commit with upstream's"}, nil
	}
	base := bases[0]
	switch strategy {
	case config.SyncMerge:
		return planMerge(repo, base, headC, targetC, sig, message)
	default:
		return planRebase(repo, base, headC, targetC)
	}
}

func planMerge(repo *git.Repository, base, head, target *object.Commit, sig object.Signature, message string) (SyncPlan, error) {
	tree, conflicts, err := mergeCommits(repo, base, head, target)
	if err != nil || len(conflicts) > 0 {
		return SyncPlan{Result: SyncConflict, Conflicts: conflicts, Reason: "upstream's changes overlap ours"}, err
	}
	h, err := writeCommit(repo, &object.Commit{
		Author: sig, Committer: sig, Message: message, TreeHash: tree,
		ParentHashes: []plumbing.Hash{head.Hash, target.Hash},
	})
	return SyncPlan{Result: SyncSynced, NewHead: h}, err
}

// planRebase replays our commits since the merge base onto upstream's commit, oldest first,
// each as a three-way merge (a cherry-pick). A commit whose change upstream already contains
// becomes empty and is dropped, as git rebase does. Authors, committers and messages are
// kept, so replaying the same history onto the same commit always yields the same hashes.
func planRebase(repo *git.Repository, base, head, target *object.Commit) (SyncPlan, error) {
	var ours []*object.Commit
	for c := head; c.Hash != base.Hash; {
		if c.NumParents() != 1 {
			return SyncPlan{Result: SyncConflict, Reason: fmt.Sprintf("our history has a merge commit (%s); a rebase replays a straight line — use strategy: merge", c.Hash.String()[:9])}, nil
		}
		ours = append(ours, c)
		p, err := c.Parent(0)
		if err != nil {
			return SyncPlan{}, err
		}
		c = p
	}
	tip := target
	for i := len(ours) - 1; i >= 0; i-- {
		c := ours[i]
		parent, err := c.Parent(0)
		if err != nil {
			return SyncPlan{}, err
		}
		tree, conflicts, err := mergeCommits(repo, parent, tip, c)
		if err != nil {
			return SyncPlan{}, err
		}
		if len(conflicts) > 0 {
			return SyncPlan{Result: SyncConflict, Conflicts: conflicts, Reason: fmt.Sprintf("our commit %s (%s) overlaps upstream's changes", c.Hash.String()[:9], firstLine(c.Message))}, nil
		}
		if tree == tip.TreeHash {
			continue // upstream already contains this change
		}
		h, err := writeCommit(repo, &object.Commit{
			Author: c.Author, Committer: c.Committer, Message: c.Message, TreeHash: tree,
			ParentHashes: []plumbing.Hash{tip.Hash},
		})
		if err != nil {
			return SyncPlan{}, err
		}
		if tip, err = repo.CommitObject(h); err != nil {
			return SyncPlan{}, err
		}
	}
	return SyncPlan{Result: SyncSynced, NewHead: tip.Hash, Rewrites: true}, nil
}

func mergeCommits(repo *git.Repository, base, ours, theirs *object.Commit) (plumbing.Hash, []string, error) {
	trees := make([]*object.Tree, 3)
	for i, c := range []*object.Commit{base, ours, theirs} {
		t, err := c.Tree()
		if err != nil {
			return plumbing.ZeroHash, nil, err
		}
		trees[i] = t
	}
	return merge.Trees(repo, trees[0], trees[1], trees[2])
}

func writeCommit(repo *git.Repository, c *object.Commit) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// BranchHead returns the commit an upstream branch points at (anonymous ls-remote).
func BranchHead(ctx context.Context, src Source, branch string) (string, error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "upstream", URLs: []string{src.Location}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("ls-remote %s: %w", src.Location, err)
	}
	want := plumbing.NewBranchReferenceName(branch)
	for _, r := range refs {
		if r.Name() == want {
			return r.Hash().String(), nil
		}
	}
	return "", fmt.Errorf("%s has no branch %s", src, branch)
}

// FetchRef is where a sync keeps upstream's fetched commit in our repository.
func FetchRef(sourceID string) plumbing.ReferenceName {
	return plumbing.ReferenceName("refs/stagefreight/upstream/" + sourceID)
}

// Fetch brings upstream's ref (a tag or branch, full name) into our repository at
// FetchRef(src.ID). History shared with ours is not transferred again.
func Fetch(ctx context.Context, repo *git.Repository, src Source, ref plumbing.ReferenceName) error {
	spec := gitconfig.RefSpec("+" + ref.String() + ":" + FetchRef(src.ID).String())
	err := repo.FetchContext(ctx, &git.FetchOptions{
		RemoteURL: src.Location,
		RefSpecs:  []gitconfig.RefSpec{spec},
		Tags:      git.NoTags,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetching %s from %s: %w", ref.Short(), src.Location, err)
	}
	return nil
}
