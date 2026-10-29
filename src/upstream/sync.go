package upstream

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/PrPlanIT/StageFreight/src/ci/render"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/lint"
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
	Result     string
	NewHead    plumbing.Hash
	Rewrites   bool     // NewHead does not descend from our head (a rebase): pushing it is a force push
	Conflicts  []string // paths (and, for a rebase, the commit) that could not be joined
	Held       []string // upstream changed these; the fork's version was kept (take: fork)
	Overridden []string // the fork's edits to these were replaced by upstream's (take: upstream)
	Dropped    []string // fork commits that became empty after a rebase (already upstream, or fully held/overridden)
	Reason     string
}

// SyncOptions configure a sync plan.
type SyncOptions struct {
	Strategy     string       // config.SyncRebase | SyncMerge | SyncMirror
	Policy       merge.Policy // per-path outcome (FilePolicy); nil merges everything
	Sig          object.Signature
	MergeMessage string // the merge commit's message (strategy merge)
	HoldMessage  string // the message of the commit that keeps fork-held files (a list follows)
}

// PlanSync joins upstream's commit target into our head.
//
// Per-file rules (upstream.sync.files) apply to rebase and merge. A file marked review that
// upstream changed makes the whole sync a conflict. A merge decides every path in its one
// merge commit. A rebase replays our commits — each keeps its own changes under the rules —
// then keeps fork-held files our commits never touched in one commit of its own on top, so
// no replayed commit silently carries a hold.
func PlanSync(repo *git.Repository, head, target plumbing.Hash, opts SyncOptions) (SyncPlan, error) {
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
	ff, err := headC.IsAncestor(targetC)
	if err != nil {
		return SyncPlan{}, err
	}
	if opts.Strategy == config.SyncMirror {
		if ff {
			return SyncPlan{Result: SyncSynced, NewHead: target}, nil
		}
		return SyncPlan{Result: SyncConflict, Reason: "our branch has diverged from upstream's; a mirror only fast-forwards, so it is kept as it is"}, nil
	}

	base := headC
	if !ff {
		bases, err := headC.MergeBase(targetC)
		if err != nil {
			return SyncPlan{}, err
		}
		if len(bases) == 0 {
			return SyncPlan{Result: SyncConflict, Reason: "our history shares no commit with upstream's"}, nil
		}
		base = bases[0]
	}
	policy := opts.Policy
	if policy == nil {
		policy = func(string) string { return merge.Merge }
	}
	upChanged, err := changedPaths(base, targetC)
	if err != nil {
		return SyncPlan{}, err
	}
	var review []string
	for _, p := range upChanged {
		if policy(p) == merge.Review {
			review = append(review, p)
		}
	}
	if len(review) > 0 {
		return SyncPlan{Result: SyncConflict, Conflicts: review, Reason: "upstream changed files marked review"}, nil
	}

	var plan SyncPlan
	tip := targetC
	switch {
	case ff:
		// Nothing of ours on top of upstream's line: upstream's commit, then any holds.
	case opts.Strategy == config.SyncMerge:
		trees, err := commitTrees(base, headC, targetC)
		if err != nil {
			return SyncPlan{}, err
		}
		res, err := merge.Trees(repo, trees[0], trees[1], trees[2], policy, true)
		if err != nil {
			// A hard failure (blob read, storage) is not a content conflict — keep them
			// distinct so the caller does not open a spurious conflict MR for an I/O fault.
			return SyncPlan{}, err
		}
		if len(res.Conflicts) > 0 {
			return SyncPlan{Result: SyncConflict, Conflicts: res.Conflicts, Reason: "upstream's changes overlap ours"}, nil
		}
		h, err := writeCommit(repo, &object.Commit{
			Author: opts.Sig, Committer: opts.Sig, Message: opts.MergeMessage, TreeHash: res.Tree,
			ParentHashes: []plumbing.Hash{head, target},
		})
		return SyncPlan{Result: SyncSynced, NewHead: h, Held: res.Held, Overridden: res.Overridden}, err
	default:
		var conflict *SyncPlan
		tip, plan, conflict, err = replay(repo, base, headC, targetC, policy)
		if err != nil || conflict != nil {
			if conflict != nil {
				return *conflict, err
			}
			return SyncPlan{}, err
		}
		plan.Rewrites = true
	}

	// Keep the fork's version of held files upstream changed, in a commit of its own.
	tip, held, err := holdFiles(repo, tip, headC, upChanged, policy, opts)
	if err != nil {
		return SyncPlan{}, err
	}
	plan.Result, plan.NewHead = SyncSynced, tip.Hash
	plan.Held = union(plan.Held, held)
	return plan, nil
}

// replay rebases our first-parent commits since base onto target, oldest first, each as a
// three-way merge (a cherry-pick) under the per-file policy. A commit whose change upstream
// already contains becomes empty and is dropped, as git rebase does. Authors, committers and
// messages are kept, so replaying the same history onto the same commit yields the same hashes.
func replay(repo *git.Repository, base, head, target *object.Commit, policy merge.Policy) (*object.Commit, SyncPlan, *SyncPlan, error) {
	var ours []*object.Commit
	for c := head; c.Hash != base.Hash; {
		if c.NumParents() != 1 {
			return nil, SyncPlan{}, &SyncPlan{Result: SyncConflict, Reason: fmt.Sprintf("our history has a merge commit (%s); a rebase replays a straight line — use strategy: merge", c.Hash.String()[:9])}, nil
		}
		ours = append(ours, c)
		p, err := c.Parent(0)
		if err != nil {
			return nil, SyncPlan{}, nil, err
		}
		c = p
	}
	var plan SyncPlan
	tip := target
	for i := len(ours) - 1; i >= 0; i-- {
		c := ours[i]
		parent, err := c.Parent(0)
		if err != nil {
			return nil, plan, nil, err
		}
		trees, err := commitTrees(parent, c, tip)
		if err != nil {
			return nil, plan, nil, err
		}
		res, err := merge.Trees(repo, trees[0], trees[1], trees[2], policy, false)
		if err != nil {
			return nil, plan, nil, err
		}
		if len(res.Conflicts) > 0 {
			return nil, plan, &SyncPlan{Result: SyncConflict, Conflicts: res.Conflicts, Reason: fmt.Sprintf("our commit %s (%s) overlaps upstream's changes", c.Hash.String()[:9], firstLine(c.Message))}, nil
		}
		plan.Held = union(plan.Held, res.Held)
		plan.Overridden = union(plan.Overridden, res.Overridden)
		if res.Tree == tip.TreeHash {
			// The commit's net change is already in tip — upstream had it, or every edit was
			// held/overridden. Record it so a dropped fork commit is visible, not silent.
			plan.Dropped = append(plan.Dropped, fmt.Sprintf("%s %s", c.Hash.String()[:9], firstLine(c.Message)))
			continue
		}
		h, err := writeCommit(repo, &object.Commit{
			Author: c.Author, Committer: c.Committer, Message: c.Message, TreeHash: res.Tree,
			ParentHashes: []plumbing.Hash{tip.Hash},
		})
		if err != nil {
			return nil, plan, nil, err
		}
		if tip, err = repo.CommitObject(h); err != nil {
			return nil, plan, nil, err
		}
	}
	return tip, plan, nil, nil
}

// holdFiles sets every fork-held path upstream changed back to the fork's version (head's),
// committing that on top of tip when anything differs.
func holdFiles(repo *git.Repository, tip, head *object.Commit, upChanged []string, policy merge.Policy, opts SyncOptions) (*object.Commit, []string, error) {
	tipTree, err := tip.Tree()
	if err != nil {
		return nil, nil, err
	}
	headTree, err := head.Tree()
	if err != nil {
		return nil, nil, err
	}
	tf, err := gitstate.FlattenTree(tipTree)
	if err != nil {
		return nil, nil, err
	}
	hf, err := gitstate.FlattenTree(headTree)
	if err != nil {
		return nil, nil, err
	}
	var held []string
	for _, p := range upChanged {
		if policy(p) != merge.Fork {
			continue
		}
		want, inHead := hf[p]
		got, inTip := tf[p]
		if inHead == inTip && (!inHead || (want.Hash == got.Hash && want.Mode == got.Mode)) {
			continue
		}
		if inHead {
			tf[p] = want
		} else {
			delete(tf, p)
		}
		held = append(held, p)
	}
	if len(held) == 0 {
		return tip, nil, nil
	}
	tree, err := gitstate.WriteTreeFromFlat(repo, tf)
	if err != nil {
		return nil, nil, err
	}
	msg := opts.HoldMessage
	if msg == "" {
		msg = "Keep the fork's version of files upstream changed"
	}
	h, err := writeCommit(repo, &object.Commit{
		Author: opts.Sig, Committer: opts.Sig, TreeHash: tree, ParentHashes: []plumbing.Hash{tip.Hash},
		Message: msg + "\n\nHeld: " + strings.Join(held, ", ") + "\nUpdated-By: StageFreight\n",
	})
	if err != nil {
		return nil, nil, err
	}
	c, err := repo.CommitObject(h)
	return c, held, err
}

// changedPaths lists the paths whose entry differs between two commits' trees.
func changedPaths(from, to *object.Commit) ([]string, error) {
	trees, err := commitTrees(from, to)
	if err != nil {
		return nil, err
	}
	a, err := gitstate.FlattenTree(trees[0])
	if err != nil {
		return nil, err
	}
	b, err := gitstate.FlattenTree(trees[1])
	if err != nil {
		return nil, err
	}
	var out []string
	for p, e := range a {
		if f, ok := b[p]; !ok || f.Hash != e.Hash || f.Mode != e.Mode {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func commitTrees(cs ...*object.Commit) ([]*object.Tree, error) {
	out := make([]*object.Tree, len(cs))
	for i, c := range cs {
		t, err := c.Tree()
		if err != nil {
			return nil, err
		}
		out[i] = t
	}
	return out, nil
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
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

// FilePolicy turns upstream.sync.files into a per-path policy: the last matching rule wins, and
// a path no rule matches is merged. StageFreight's own files — its config, its namespace and
// every rendered CI file — always keep the fork's version: they are generated for this repo,
// and upstream's copies (its own CI, say) must never be merged into them.
func FilePolicy(rules []config.UpstreamFileRule) merge.Policy {
	owned := []string{".stagefreight.yml", ".stagefreight/**"}
	for _, forge := range render.SupportedForges {
		if p, err := render.ForgeTarget(forge); err == nil {
			owned = append(owned, p)
		}
	}
	return func(path string) string {
		for _, o := range owned {
			if lint.MatchGlob(o, path) {
				return merge.Fork
			}
		}
		take := merge.Merge
		for _, r := range rules {
			if lint.MatchGlob(r.Match, path) {
				take = r.Take
			}
		}
		return take
	}
}
