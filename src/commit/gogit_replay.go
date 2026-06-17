package commit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/utils/merkletrie"
)

const sfGeneratedTrailer = "X-StageFreight-Generated: true"

// Replay rebases local commits onto upstream using controlled tree replay.
// It is NOT a raw git rebase — it replays only SF-generated commits via
// explicit tree diffing with full integrity verification at each step.
//
// Algorithm:
//  0. Pre-conditions: HEAD attached, worktree clean, upstream configured → fail fast, no mutation
//  1. Gate: no merge commits, linear chain (structural only — no authorship constraints)
//  2. Re-validate upstream hash non-zero; record fetchedUpstreamHash
//  3. Compute merge-base (exactly 1); collect commits oldest-first
//  4. Hard reset to upstream
//  5. For each commit: apply diff, stage, verify staging states, commit
//  6. Race guard: upstream unchanged since fetch
//
// Push is NOT performed by Replay — the engine (Engine.doReplayThenPush) owns push
// so the transition DIVERGED → REPLAY → CLEAN_AHEAD → PUSH remains in the engine's
// state machine and the push is logged as a formal transition.
//
// Hooks are NOT run during replay — replay commits are machine-generated
// re-applications; running hooks again would double-execute side effects.
func Replay(session *gitstate.SyncSession) error {
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

	// Pre-condition: worktree must be clean
	wtStatus, err := wt.Status()
	if err != nil {
		return fmt.Errorf("checking worktree status: %w", err)
	}
	if !wtStatus.IsClean() {
		return gitstate.ErrDirtyWorktree
	}

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

	// Get repo root for filesystem operations
	repoRoot := wt.Filesystem.Root()

	// 5. Hard reset to upstream — mutation begins here
	if err := wt.Reset(&git.ResetOptions{
		Commit: fetchedUpstreamHash,
		Mode:   git.HardReset,
	}); err != nil {
		return fmt.Errorf("hard reset to upstream: %w", err)
	}

	// 6. Replay commits oldest-first
	for _, c := range commits {
		if err := replayCommit(repo, wt, repoRoot, c, originalHEAD); err != nil {
			_ = wt.Reset(&git.ResetOptions{Commit: originalHEAD, Mode: git.HardReset})
			return err
		}
	}

	// 7. Race guard: upstream must not have moved since our fetch
	if err := session.Refresh(); err != nil {
		_ = wt.Reset(&git.ResetOptions{Commit: originalHEAD, Mode: git.HardReset})
		return fmt.Errorf("refreshing state before push: %w", err)
	}
	if session.State().UpstreamHash != fetchedUpstreamHash {
		_ = wt.Reset(&git.ResetOptions{Commit: originalHEAD, Mode: git.HardReset})
		return gitstate.ErrUpstreamMoved
	}

	// Push is the engine's responsibility — Replay() owns only the rebase.
	// The caller (Engine.doReplayThenPush) will call doPush() after Replay returns.
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

// replayCommit applies a single commit's diff to the worktree and creates a new commit.
// Hooks are NOT run — replay is a machine operation, not a user commit.
// On error, the caller is responsible for resetting to originalHEAD.
func replayCommit(repo *git.Repository, wt *git.Worktree, repoRoot string, c *object.Commit, originalHEAD plumbing.Hash) error {
	var parentTree *object.Tree
	if len(c.ParentHashes) > 0 {
		parentCommit, err := repo.CommitObject(c.ParentHashes[0])
		if err != nil {
			return fmt.Errorf("loading parent commit: %w", err)
		}
		parentTree, err = parentCommit.Tree()
		if err != nil {
			return fmt.Errorf("loading parent tree: %w", err)
		}
	}

	commitTree, err := c.Tree()
	if err != nil {
		return fmt.Errorf("loading commit tree: %w", err)
	}

	var changes object.Changes
	if parentTree != nil {
		changes, err = parentTree.Diff(commitTree)
	} else {
		emptyTree := &object.Tree{}
		changes, err = emptyTree.Diff(commitTree)
	}
	if err != nil {
		return fmt.Errorf("computing diff: %w", err)
	}

	for _, change := range changes {
		if err := applyChange(repoRoot, change); err != nil {
			return err
		}
	}

	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("staging changes: %w", err)
	}

	// Diff-application sanity check.
	//
	// We do NOT compare tree hashes to the original commit — after rebasing on a new
	// upstream base the replayed tree includes upstream changes, so the hashes will
	// legitimately differ. Instead we verify that the diff applied without producing
	// any unexpected or corrupted staging states (conflicts, unknown modes, etc.).
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("checking worktree status after staging: %w", err)
	}
	for path, s := range status {
		switch s.Staging {
		case git.Unmodified, git.Added, git.Modified, git.Deleted:
			// Valid states — diff applied cleanly.
		default:
			_ = wt.Reset(&git.ResetOptions{Commit: originalHEAD, Mode: git.HardReset})
			return fmt.Errorf("unexpected staging state for %s (%v) after applying commit %s — hard reset applied",
				path, s.Staging, c.Hash.String()[:8])
		}
	}

	// Path-integrity guard (data-corruption defense). The replay reconstructs files
	// from a diff; a wrong-field path bug (object.File.Name basename vs ChangeEntry
	// full path) silently writes nested files to the repo root, producing a commit
	// with the wrong tree — or an empty one. Verify the STAGED path-set equals the
	// source commit's CHANGE path-set. A non-empty source replaying to empty fails
	// here too (empty set != non-empty set). On mismatch the caller hard-resets to
	// originalHEAD, so corruption can never become a commit, let alone a push.
	sourceEmpty := len(changes) == 0
	if !sourceEmpty {
		expected := changePathSet(changes)
		actual := stagedPathSet(status)
		if !equalStringSet(expected, actual) {
			return &ErrReplayCorruption{
				Commit:   c.Hash.String()[:8],
				Expected: sortedKeys(expected),
				Actual:   sortedKeys(actual),
			}
		}
	}

	// Commit, preserving original Author; Committer timestamp = now (replay time).
	// No hooks — replay is machine-generated. AllowEmptyCommits is true ONLY when the
	// source commit was itself empty; a non-empty source replaying to empty was
	// already rejected by the path-integrity guard above.
	now := time.Now()
	newHash, err := wt.Commit(c.Message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  c.Author.Name,
			Email: c.Author.Email,
			When:  c.Author.When,
		},
		Committer: &object.Signature{
			Name:  c.Committer.Name,
			Email: c.Committer.Email,
			When:  now,
		},
		AllowEmptyCommits: sourceEmpty,
	})
	if err != nil {
		return fmt.Errorf("creating replayed commit: %w", err)
	}

	// Change-set equivalence gate — the authoritative tree-identity check, beyond
	// the path-set guard above. The delta the replayed commit introduces over its
	// base (diff(baseTree, replayedTree)) MUST equal the source commit's delta
	// entry-for-entry: operation kind, old/new path, blob OID, and file mode. Both
	// deltas share the same base tree, so identical structured change-sets prove the
	// resulting trees are bit-identical — collapsing blob-content, mode, exec-bit,
	// symlink, rename, and subtree-composition corruption into one invariant that
	// path-set equivalence alone cannot see. Authoritative and publish-blocking: on
	// mismatch the caller (Replay loop) hard-resets to originalHEAD, so a
	// non-equivalent replay can never become a commit that is pushed.
	if !sourceEmpty {
		replayed, derr := replayedDelta(repo, newHash)
		if derr != nil {
			return fmt.Errorf("computing replayed delta for verification: %w", derr)
		}
		if !equalChangeSets(changes, replayed) {
			return &ErrReplayCorruption{
				Commit:   c.Hash.String()[:8],
				Expected: changeSignatures(changes),
				Actual:   changeSignatures(replayed),
			}
		}
	}

	return nil
}

// ErrReplayCorruption signals that a replayed commit's delta did not match the
// source commit's delta — a structural corruption. Raised by both the pre-commit
// path-set guard (e.g. nested files written to the repo root by basename) and the
// post-commit change-set equivalence gate (blob/mode/op divergence at preserved
// paths). Expected/Actual hold the source vs replayed signatures. Never pushed.
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
	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("loading replayed tree: %w", err)
	}
	if c.NumParents() == 0 {
		return (&object.Tree{}).Diff(tree)
	}
	parent, err := c.Parent(0)
	if err != nil {
		return nil, fmt.Errorf("loading replay base commit: %w", err)
	}
	baseTree, err := parent.Tree()
	if err != nil {
		return nil, fmt.Errorf("loading replay base tree: %w", err)
	}
	return baseTree.Diff(tree)
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

// stagedPathSet is the set of paths the worktree has actually staged.
func stagedPathSet(status git.Status) map[string]bool {
	set := make(map[string]bool)
	for path, s := range status {
		switch s.Staging {
		case git.Added, git.Modified, git.Deleted:
			set[path] = true
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

// applyChange applies a single tree change to the worktree filesystem.
//
// CRITICAL: filesystem paths MUST come from change.From.Name / change.To.Name —
// the ChangeEntry's FULL tree path. object.File.Name (from change.Files()) is the
// entry BASENAME, not the path; using it writes nested files to the repo root,
// which is a data-corruption bug. The *object.File is used ONLY for content/mode.
func applyChange(repoRoot string, change *object.Change) error {
	action, err := change.Action()
	if err != nil {
		return fmt.Errorf("determining change action: %w", err)
	}

	from, to, err := change.Files()
	if err != nil {
		return fmt.Errorf("getting change files: %w", err)
	}

	fromPath := change.From.Name // full tree path (NOT from.Name basename)
	toPath := change.To.Name     // full tree path (NOT to.Name basename)

	switch action {
	case merkletrie.Insert:
		if to == nil {
			return nil
		}
		return writeFile(repoRoot, toPath, to)

	case merkletrie.Delete:
		if from == nil {
			return nil
		}
		if err := checkPathSafe(repoRoot, fromPath); err != nil {
			return err
		}
		dest := filepath.Join(repoRoot, fromPath)
		// For existing paths, verify the real parent dir hasn't been redirected
		// by a pre-existing symlink to outside the repo root.
		if err := checkRealPathSafe(repoRoot, filepath.Dir(dest), fromPath); err != nil {
			return err
		}
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("deleting %s: %w", fromPath, err)
		}

	case merkletrie.Modify:
		// Rename: delete old path, write new path
		if from != nil && to != nil && fromPath != toPath {
			if err := checkPathSafe(repoRoot, fromPath); err != nil {
				return err
			}
			oldPath := filepath.Join(repoRoot, fromPath)
			if err := checkRealPathSafe(repoRoot, filepath.Dir(oldPath), fromPath); err != nil {
				return err
			}
			if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing old path %s: %w", fromPath, err)
			}
		}
		if to != nil {
			return writeFile(repoRoot, toPath, to)
		}
	}

	return nil
}

// writeFile writes a file object's content to path (a full repo-relative tree
// path) under repoRoot. path is authoritative for the destination; f supplies
// only content and mode. Handles regular files, executable files, and symlinks.
func writeFile(repoRoot, path string, f *object.File) error {
	if err := checkPathSafe(repoRoot, path); err != nil {
		return err
	}
	dest := filepath.Join(repoRoot, path)

	switch f.Mode {
	case filemode.Symlink:
		// Symlink: blob content is the target path.
		// Validate that the symlink target, resolved relative to its parent directory,
		// does not escape the repository root.
		content, err := f.Contents()
		if err != nil {
			return fmt.Errorf("reading symlink target for %s: %w", path, err)
		}
		target := strings.TrimSpace(content)

		// Resolve the symlink target relative to its parent directory
		parentDir := filepath.Dir(dest)
		resolvedTarget := target
		if !filepath.IsAbs(target) {
			resolvedTarget = filepath.Join(parentDir, target)
		}
		resolvedTarget = filepath.Clean(resolvedTarget)

		// The resolved target must stay within the repo root
		rootWithSep := repoRoot + string(os.PathSeparator)
		if resolvedTarget != repoRoot && !strings.HasPrefix(resolvedTarget, rootWithSep) {
			return &gitstate.ErrPathTraversal{Path: path + " -> " + target}
		}

		_ = os.Remove(dest) // remove any existing file
		if err := os.Symlink(target, dest); err != nil {
			return fmt.Errorf("creating symlink %s: %w", path, err)
		}

	default:
		// Regular file or executable
		osMode, err := f.Mode.ToOSFileMode()
		if err != nil {
			osMode = 0o644 // safe default
		}
		parentDir := filepath.Dir(dest)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("creating parent dirs for %s: %w", path, err)
		}
		// Post-MkdirAll: resolve symlinks in the real parent dir to catch
		// pre-existing symlinked directories that escape the repo root.
		if err := checkRealPathSafe(repoRoot, parentDir, path); err != nil {
			return err
		}
		reader, err := f.Blob.Reader()
		if err != nil {
			return fmt.Errorf("reading blob for %s: %w", path, err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return fmt.Errorf("reading blob data for %s: %w", path, err)
		}
		if err := os.WriteFile(dest, data, osMode); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// checkPathSafe performs a lexical path-traversal check.
// It guards against ".." escape via filepath.Clean but does NOT resolve symlinks.
// For filesystem writes, call checkRealPathSafe after the parent dir exists.
func checkPathSafe(repoRoot, relPath string) error {
	absPath := filepath.Join(repoRoot, filepath.Clean(relPath))
	rootWithSep := repoRoot + string(os.PathSeparator)
	if absPath != repoRoot && !strings.HasPrefix(absPath, rootWithSep) {
		return &gitstate.ErrPathTraversal{Path: relPath}
	}
	return nil
}

// checkRealPathSafe resolves all symlinks in dirPath and verifies the result
// is still within repoRoot. This catches escapes via pre-existing symlinked
// directories that a lexical check cannot detect.
// dirPath must exist on disk (call after MkdirAll or only for existing paths).
func checkRealPathSafe(repoRoot, dirPath, label string) error {
	real, err := filepath.EvalSymlinks(dirPath)
	if err != nil {
		// Path disappeared between MkdirAll and here — treat as traversal-safe,
		// the subsequent write/remove will fail on its own with a clear OS error.
		return nil
	}
	rootWithSep := repoRoot + string(os.PathSeparator)
	if real != repoRoot && !strings.HasPrefix(real, rootWithSep) {
		return &gitstate.ErrPathTraversal{Path: label + " (parent dir resolves outside repo root: " + real + ")"}
	}
	return nil
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
