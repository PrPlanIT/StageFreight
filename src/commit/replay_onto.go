package commit

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/gitstate"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Two replay mechanisms share one verification vocabulary (changeSignature,
// ErrReplayCorruption) under different contracts:
//
//   - Replay (gogit_replay.go): sequential user-commit chains rebuilt at the object
//     layer (replayOnto), gated by STRICT delta equality (any divergence from the
//     source delta is corruption), then checked out once over the differing paths.
//
//   - replayCommitOntoTip (this file): OBJECT mechanism — one machine-generated
//     commit rebuilt onto a moved branch tip with no worktree involvement,
//     gated by PATH-SCOPED CONTAINMENT: every path the replayed commit touches
//     must land exactly the source commit's content, and every path outside the
//     source change-set must be preserved from the tip. Strict equality is
//     deliberately NOT required — overlapping machine-owned paths (two scribe
//     renders racing) are last-writer-wins by design, since every render
//     recomputes those regions from live state.

// replayCommitOntoTip rebuilds one commit on a new parent at the object layer.
// The commit's change-set (diff against its own parent) is applied to the tip's
// tree and a new commit is written with the same author, committer, and message.
// Returns the commit unchanged when its parent already IS the tip (the caller's
// push merely raced and can retry as-is).
func replayCommitOntoTip(repo *git.Repository, local, tip plumbing.Hash) (plumbing.Hash, error) {
	localC, err := repo.CommitObject(local)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("loading local commit: %w", err)
	}
	if localC.NumParents() != 1 {
		return plumbing.ZeroHash, fmt.Errorf("object replay supports single-parent commits; %s has %d parents", short(local), localC.NumParents())
	}
	parentC, err := localC.Parent(0)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("loading local parent: %w", err)
	}
	if parentC.Hash == tip {
		return local, nil
	}
	tipC, err := repo.CommitObject(tip)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("loading tip commit: %w", err)
	}

	parentTree, err := parentC.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	localTree, err := localC.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	tipTree, err := tipC.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	sourceDelta, err := parentTree.Diff(localTree)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("diffing source commit: %w", err)
	}
	if len(sourceDelta) == 0 {
		return plumbing.ZeroHash, fmt.Errorf("source commit %s has an empty change-set", short(local))
	}

	flat, err := gitstate.FlattenTree(tipTree)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("flattening tip tree: %w", err)
	}
	applyChangesToFlat(flat, sourceDelta)

	newTreeHash, err := gitstate.WriteTreeFromFlat(repo, flat)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writing replayed tree: %w", err)
	}

	newHash, err := writeCommit(repo, localC, newTreeHash, tip, localC.Committer)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	// Containment gate — same vocabulary as the worktree replay's equivalence
	// gate, adapted to this mechanism's contract. The replayed delta
	// (diff(tip, new)) may be SMALLER than the source delta (a delete of a path
	// the tip lacks, a modify the tip already carries), but every change in it
	// must be authorized by a source change landing the same end-state.
	replayed, err := replayedDelta(repo, newHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("verifying replayed commit: %w", err)
	}
	if err := verifyContainment(sourceDelta, replayed, short(local)); err != nil {
		return plumbing.ZeroHash, err
	}
	return newHash, nil
}

// verifyContainment asserts every replayed change is authorized by a source
// change with the same end-state: inserts/modifies must land the source's exact
// blob+mode at a source-touched path, deletions must correspond to a source
// deletion (or rename departure) of that path. Violations raise
// ErrReplayCorruption with the shared signature rendering — never pushed.
func verifyContainment(source, replayed object.Changes, commit string) error {
	authorizedTo := map[string]object.TreeEntry{} // path → end-state the source lands
	departed := map[string]bool{}                 // paths the source removes content from
	for _, ch := range source {
		if ch.To.Name != "" {
			authorizedTo[ch.To.Name] = ch.To.TreeEntry
		}
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			departed[ch.From.Name] = true
		}
	}

	var violations []string
	for _, ch := range replayed {
		switch {
		case ch.To.Name != "":
			want, ok := authorizedTo[ch.To.Name]
			if !ok || want.Hash != ch.To.TreeEntry.Hash || want.Mode != ch.To.TreeEntry.Mode {
				violations = append(violations, changeSignature(ch))
			}
			if ch.From.Name != "" && ch.From.Name != ch.To.Name && !departed[ch.From.Name] {
				violations = append(violations, changeSignature(ch))
			}
		case ch.From.Name != "":
			if !departed[ch.From.Name] {
				violations = append(violations, changeSignature(ch))
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	expected := make([]string, 0, len(source))
	for _, ch := range source {
		expected = append(expected, changeSignature(ch))
	}
	return &ErrReplayCorruption{Commit: commit, Expected: expected, Actual: violations}
}

func short(h plumbing.Hash) string { return h.String()[:8] }
