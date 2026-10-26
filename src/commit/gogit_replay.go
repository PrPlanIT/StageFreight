package commit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

const sfGeneratedTrailer = "X-StageFreight-Generated: true"

// Replay rebases local commits onto upstream using controlled tree replay.
// It is NOT a raw git rebase — it replays only SF-generated commits via
// explicit tree diffing with full integrity verification at each step.
//
// Algorithm:
//  0. Pre-conditions: HEAD attached, upstream configured → fail fast, no mutation
//  1. Gate: no merge commits, linear chain (structural only — no authorship constraints)
//  2. Re-validate upstream hash non-zero; record fetchedUpstreamHash
//  3. Compute merge-base (exactly 1); collect commits oldest-first
//  4. Build each replayed commit at the object layer; verify its delta equals the source's
//  5. Race guard: upstream unchanged since fetch, branch unchanged since read
//  6. Refuse, right before the write, if a path that differs between HEAD and the
//     replayed head holds uncommitted work or a file git does not track there
//  7. Move the branch to the replayed head and check out only the differing paths;
//     everything else in the index and worktree is left as the operator had it
//
// Push is NOT performed by Replay — the planner's executor owns it: a diverged plan is the
// operation graph Confirm → Replay → Upload, so Execute runs the Upload after Replay returns.
// Replay only rewrites local history; the push stays a separate, gated operation.
//
// Hooks are NOT run during replay — replay commits are machine-generated
// re-applications; running hooks again would double-execute side effects.
func Replay(session *gitstate.SyncSession) (err error) {
	repo := session.Repo()
	state := session.State()

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("opening worktree: %w", err)
	}

	// Pre-condition: HEAD must be attached to a branch
	if state.DetachedHEAD {
		return gitstate.ErrDetachedHEAD
	}

	// Pre-condition: upstream tracking config must exist before we fetch.
	// UpstreamHash may be zero here if the remote-tracking ref has never been
	// fetched (e.g. fresh clone without fetch, or first push of a new branch) —
	// that is fine; we check the hash again after fetch. What we require here
	// is that a tracking remote+merge ref is configured in .git/config.
	if !state.UpstreamConfigured {
		return gitstate.ErrNoUpstream
	}

	// Uncommitted work is NOT a pre-condition failure, as with git rebase --autostash:
	// the replay writes only the paths that differ between HEAD and the replayed head
	// and refuses (step 5) only if one of those holds uncommitted work.

	originalHEAD := state.HeadHash

	// The Engine already called session.Fetch() before calling Replay().
	// session.State() reflects the post-fetch upstream — no re-fetch needed.
	// Re-validate upstream hash is non-zero (safety check, should never fail here).
	if state.UpstreamHash.IsZero() {
		return gitstate.ErrNoUpstream
	}
	fetchedUpstreamHash := state.UpstreamHash

	// 2. Compute merge-base and collect local commits oldest-first
	headCommit, err := repo.CommitObject(originalHEAD)
	if err != nil {
		return fmt.Errorf("loading HEAD commit: %w", err)
	}
	upstreamCommit, err := repo.CommitObject(fetchedUpstreamHash)
	if err != nil {
		return fmt.Errorf("loading upstream commit: %w", err)
	}

	bases, err := headCommit.MergeBase(upstreamCommit)
	if err != nil {
		return fmt.Errorf("computing merge base: %w", err)
	}
	// Exactly one merge-base required. Multiple bases indicate a criss-cross merge
	// history; zero bases indicate unrelated histories — both are unsafe for replay.
	if len(bases) != 1 {
		return &gitstate.ErrReplayUnsafe{Reasons: []string{
			fmt.Sprintf("expected exactly 1 merge base, found %d — "+
				"criss-cross or unrelated histories cannot be replayed automatically", len(bases)),
		}}
	}
	mergeBase := bases[0].Hash

	var commits []*object.Commit
	logIter, err := repo.Log(&git.LogOptions{From: originalHEAD})
	if err != nil {
		return fmt.Errorf("walking local commits: %w", err)
	}
	_ = logIter.ForEach(func(c *object.Commit) error {
		if c.Hash == mergeBase {
			return storer.ErrStop
		}
		commits = append(commits, c)
		return nil
	})

	if len(commits) == 0 {
		// No local commits between HEAD and the merge base — nothing to replay.
		// The engine should have classified this as CLEAN_SYNCED before calling
		// Replay; reaching here is a caller bug.
		return fmt.Errorf("replay called with no local commits to replay (state should have been CLEAN_SYNCED)")
	}

	// Reverse to oldest-first
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	// 3. Gate: validate ALL commits before any mutation; collect all violations
	if violations := validateReplayGate(commits, mergeBase); len(violations) > 0 {
		return &gitstate.ErrReplayUnsafe{Reasons: violations}
	}

	// 4. Build every replayed commit at the object layer and verify it. Nothing
	// touches the branch, the index or the worktree until all of them pass.
	originalTree, err := headCommit.Tree()
	if err != nil {
		return fmt.Errorf("loading HEAD tree: %w", err)
	}
	upstreamTree, err := upstreamCommit.Tree()
	if err != nil {
		return fmt.Errorf("loading upstream tree: %w", err)
	}
	flat, err := flattenTree(upstreamTree)
	if err != nil {
		return fmt.Errorf("flattening upstream tree: %w", err)
	}
	tip := fetchedUpstreamHash
	for _, c := range commits {
		if tip, err = replayOnto(repo, c, tip, flat); err != nil {
			return err
		}
	}
	finalCommit, err := repo.CommitObject(tip)
	if err != nil {
		return fmt.Errorf("loading replayed head: %w", err)
	}
	finalTree, err := finalCommit.Tree()
	if err != nil {
		return fmt.Errorf("loading replayed tree: %w", err)
	}

	// 5. Race guard: upstream must not have moved since our fetch, nor the branch since
	// we read it (a commit made meanwhile would be orphaned by moving the branch).
	if err := session.Refresh(); err != nil {
		return fmt.Errorf("refreshing state before push: %w", err)
	}
	if session.State().UpstreamHash != fetchedUpstreamHash {
		return gitstate.ErrUpstreamMoved
	}
	if session.State().HeadHash != originalHEAD {
		return fmt.Errorf("the branch moved during the replay (now %s) — retry", short(session.State().HeadHash))
	}

	// 6. The worktree moves from the original tree to the final one, and only the paths
	// that differ are written. Refuse, before any write, when one of them holds work the
	// operator has not committed or a file git does not track there.
	toFinal, err := originalTree.Diff(finalTree)
	if err != nil {
		return fmt.Errorf("diffing onto the replayed head: %w", err)
	}
	written := changePathSet(toFinal)
	head, err := flattenTree(originalTree)
	if err != nil {
		return fmt.Errorf("flattening HEAD tree: %w", err)
	}
	uncommitted, err := uncommittedPaths(repo, wt, head)
	if err != nil {
		return err
	}
	var conflicts []string
	for p := range written {
		if uncommitted[p] {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("uncommitted changes to %s conflict with the rebase onto upstream — commit or set them aside first, then retry", strings.Join(conflicts, ", "))
	}
	owned, err := operatorCollisions(repo, wt.Filesystem.Root(), head, written)
	if err != nil {
		return err
	}
	if len(owned) > 0 {
		return fmt.Errorf("the rebase would overwrite or remove %s, which git does not track here — move them aside, then retry", strings.Join(owned, ", "))
	}

	// 7. Move the branch to the verified head and check out only the differing paths.
	// A failure puts those paths and the branch back on the original HEAD.
	if err := checkoutPaths(wt, tip, written); err != nil {
		if rerr := checkoutPaths(wt, originalHEAD, written); rerr != nil {
			return fmt.Errorf("checking out the replayed head: %w (rolling back to %s also failed: %v)", err, short(originalHEAD), rerr)
		}
		return fmt.Errorf("checking out the replayed head: %w", err)
	}

	// Push is the executor's responsibility — Replay() owns only the rebase. The Upload
	// operation that follows Replay in the plan graph performs the push after Replay returns.
	return nil
}

// validateReplayGate validates all commits against gate conditions.
// Collects ALL violations before returning — no short-circuit.
//
// Gate is structural only: merge commits and non-linear chains cannot be
// deterministically replayed by diff application. Authorship markers (e.g.
// SF-generated trailer) are NOT gate conditions — historical commits and CI
// commits that predate the trailer are replayable by the same mechanism.
func validateReplayGate(commits []*object.Commit, mergeBase plumbing.Hash) []string {
	var violations []string
	for i, c := range commits {
		// Rule 1: no merge commits
		if len(c.ParentHashes) != 1 {
			violations = append(violations, fmt.Sprintf(
				"%s %q: has %d parents (merge commits cannot be replayed)",
				c.Hash.String()[:8], firstLine(c.Message), len(c.ParentHashes),
			))
		}
		// Rule 2: linear chain
		if len(c.ParentHashes) == 1 {
			expected := mergeBase
			if i > 0 {
				expected = commits[i-1].Hash
			}
			if c.ParentHashes[0] != expected {
				violations = append(violations, fmt.Sprintf(
					"%s %q: parent %s != expected %s (non-linear chain)",
					c.Hash.String()[:8], firstLine(c.Message),
					c.ParentHashes[0].String()[:8], expected.String()[:8],
				))
			}
		}
	}
	return violations
}

// ErrReplayCorruption signals that a replayed commit's delta did not match the
// source commit's delta — a structural corruption. Raised by the path-set guard
// (the replay touched other paths than the source) and the change-set equivalence
// gate (blob/mode/op divergence at preserved paths). Expected/Actual hold the source
// vs replayed signatures. Never pushed.
type ErrReplayCorruption struct {
	Commit   string
	Expected []string
	Actual   []string
}

func (e *ErrReplayCorruption) Error() string {
	return fmt.Sprintf("replay corruption in commit %s: replayed delta %v != source delta %v",
		e.Commit, e.Actual, e.Expected)
}

// replayedDelta returns the change-set a replayed commit introduces over its
// parent — diff(baseTree, replayedTree). Compared to the source commit's delta,
// equality (same base) proves the resulting trees are identical.
func replayedDelta(repo *git.Repository, commitHash plumbing.Hash) (object.Changes, error) {
	c, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, fmt.Errorf("loading replayed commit: %w", err)
	}
	return commitChanges(c)
}

// changeSignature renders one change as a structured, object-layer identity:
// operation kind, old path, new path, blob OIDs, and file modes — NOT patch text.
// Change-sets with equal signature multisets describe identical tree deltas.
func changeSignature(ch *object.Change) string {
	action, _ := ch.Action()
	return fmt.Sprintf("%s|%s->%s|%s->%s|%s->%s",
		action,
		ch.From.Name, ch.To.Name,
		ch.From.TreeEntry.Hash, ch.To.TreeEntry.Hash,
		ch.From.TreeEntry.Mode, ch.To.TreeEntry.Mode,
	)
}

// equalChangeSets reports whether two change-sets are identical as multisets of
// structured change signatures: same operations, paths, blob OIDs, and modes —
// no extra, no missing.
func equalChangeSets(a, b object.Changes) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, ch := range a {
		counts[changeSignature(ch)]++
	}
	for _, ch := range b {
		sig := changeSignature(ch)
		counts[sig]--
		if counts[sig] < 0 {
			return false
		}
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// changeSignatures returns the sorted structured signatures of a change-set, for
// corruption diagnostics.
func changeSignatures(changes object.Changes) []string {
	out := make([]string, 0, len(changes))
	for _, ch := range changes {
		out = append(out, changeSignature(ch))
	}
	sort.Strings(out)
	return out
}

// changePathSet is the set of full repo-relative paths a commit's diff touches,
// taken from the ChangeEntry (From/To.Name) — the authoritative full tree path,
// never object.File.Name (a basename).
func changePathSet(changes object.Changes) map[string]bool {
	set := make(map[string]bool, len(changes))
	for _, ch := range changes {
		if ch.From.Name != "" {
			set[ch.From.Name] = true
		}
		if ch.To.Name != "" {
			set[ch.To.Name] = true
		}
	}
	return set
}

func equalStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstLine returns the first non-empty line of a string (for error display).
func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 72 {
		s = s[:72] + "…"
	}
	return s
}
