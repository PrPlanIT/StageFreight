package commit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// The replay builds every commit at the object layer, then moves the worktree once,
// through go-git's checkout scoped to the paths that differ between the original and
// the final commit (ResetOptions.Files). An unscoped hard reset is never used: it
// deletes every file git does not track.

// commitChanges is a commit's diff against its first parent (the empty tree for a root).
func commitChanges(c *object.Commit) (object.Changes, error) {
	ct, err := c.Tree()
	if err != nil {
		return nil, err
	}
	parent := &object.Tree{}
	if c.NumParents() > 0 {
		p, err := c.Parent(0)
		if err != nil {
			return nil, err
		}
		if parent, err = p.Tree(); err != nil {
			return nil, err
		}
	}
	return parent.Diff(ct)
}

// replayOnto writes c's change-set onto tip as a new commit (c's author and message,
// committed now) and verifies it: the replayed delta must equal c's delta entry for
// entry. flat is tip's flattened tree; it is updated to the new commit's tree. A path
// upstream also changed is a rebase conflict, refused before anything is written.
func replayOnto(repo *git.Repository, c *object.Commit, tip plumbing.Hash, flat map[string]object.TreeEntry) (plumbing.Hash, error) {
	changes, err := commitChanges(c)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("diffing commit %s: %w", short(c.Hash), err)
	}
	if len(changes) > 0 && alreadyApplied(changes, flat) {
		return tip, nil // upstream already carries this exact change; git drops it too
	}
	if overlap := upstreamOverlap(changes, flat); len(overlap) > 0 {
		return plumbing.ZeroHash, &gitstate.ErrReplayUnsafe{Reasons: []string{fmt.Sprintf(
			"%s %q: upstream also changed %s — rebase by hand", short(c.Hash), firstLine(c.Message), strings.Join(overlap, ", "))}}
	}
	applyChangesToFlat(flat, changes)
	if clash := pathClashes(flat); len(clash) > 0 {
		return plumbing.ZeroHash, &gitstate.ErrReplayUnsafe{Reasons: []string{fmt.Sprintf(
			"%s %q: %s would be both a file and a directory — rebase by hand", short(c.Hash), firstLine(c.Message), strings.Join(clash, ", "))}}
	}
	treeHash, err := writeTreeFromFlat(repo, flat)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writing replayed tree: %w", err)
	}
	committer := c.Committer
	committer.When = time.Now()
	newHash, err := writeCommit(repo, c, treeHash, tip, committer)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	// Verify before anything reaches the branch or the worktree:
	//   - Path-set guard: the replayed delta touches exactly the source's paths (a
	//     non-empty source replaying to empty fails here too).
	//   - Change-set equivalence: entry-for-entry (op, old/new path, blob OID, mode)
	//     against the source delta; both share the base tree, so equality proves the
	//     trees are bit-identical — blob, mode, exec-bit, symlink, rename and subtree
	//     corruption collapse into one invariant the path set alone cannot see.
	replayed, err := replayedDelta(repo, newHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("computing replayed delta for verification: %w", err)
	}
	if expected, actual := changePathSet(changes), changePathSet(replayed); !equalStringSet(expected, actual) {
		return plumbing.ZeroHash, &ErrReplayCorruption{Commit: short(c.Hash), Expected: sortedKeys(expected), Actual: sortedKeys(actual)}
	}
	if !equalChangeSets(changes, replayed) {
		return plumbing.ZeroHash, &ErrReplayCorruption{Commit: short(c.Hash), Expected: changeSignatures(changes), Actual: changeSignatures(replayed)}
	}
	return newHash, nil
}

// alreadyApplied reports that tip already holds every end state the change-set lands.
func alreadyApplied(changes object.Changes, tip map[string]object.TreeEntry) bool {
	for _, ch := range changes {
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			if _, ok := tip[ch.From.Name]; ok {
				return false
			}
		}
		if ch.To.Name != "" {
			e, ok := tip[ch.To.Name]
			if !ok || e.Hash != ch.To.TreeEntry.Hash || e.Mode != ch.To.TreeEntry.Mode {
				return false
			}
		}
	}
	return true
}

// upstreamOverlap lists paths where tip no longer matches what the change expects to
// start from: a modified or deleted path tip changed or removed, or an added path tip
// already has. That is upstream touching the same path (including the same change).
func upstreamOverlap(changes object.Changes, tip map[string]object.TreeEntry) []string {
	found := map[string]bool{}
	for _, ch := range changes {
		if ch.From.Name != "" {
			if e, ok := tip[ch.From.Name]; !ok || e.Hash != ch.From.TreeEntry.Hash || e.Mode != ch.From.TreeEntry.Mode {
				found[ch.From.Name] = true
			}
		}
		if ch.To.Name != "" && ch.To.Name != ch.From.Name {
			if _, ok := tip[ch.To.Name]; ok {
				found[ch.To.Name] = true
			}
		}
	}
	return sortedKeys(found)
}

// applyChangesToFlat applies a change-set to a flattened tree in place.
func applyChangesToFlat(flat map[string]object.TreeEntry, changes object.Changes) {
	for _, ch := range changes {
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			delete(flat, ch.From.Name) // delete, or the departure side of a rename
		}
		if ch.To.Name != "" {
			flat[ch.To.Name] = object.TreeEntry{Name: ch.To.Name, Mode: ch.To.TreeEntry.Mode, Hash: ch.To.TreeEntry.Hash}
		}
	}
}

// writeCommit stores a commit with src's author and message on tree over parent.
func writeCommit(repo *git.Repository, src *object.Commit, tree, parent plumbing.Hash, committer object.Signature) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Commit{
		Author:       src.Author,
		Committer:    committer,
		Message:      src.Message,
		TreeHash:     tree,
		ParentHashes: []plumbing.Hash{parent},
	}).Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encoding replayed commit: %w", err)
	}
	h, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("storing replayed commit: %w", err)
	}
	return h, nil
}

// pathClashes lists paths that are a file in flat while also the parent directory of
// another entry; git cannot represent both.
func pathClashes(flat map[string]object.TreeEntry) []string {
	found := map[string]bool{}
	for p := range flat {
		for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
			if _, ok := flat[dir]; ok {
				found[dir] = true
			}
		}
	}
	return sortedKeys(found)
}

// checkoutPaths points the branch at `to` and makes exactly the listed paths match it,
// in the index and the worktree. Every other index entry and worktree file is left as
// it is, so the operator's staged and unstaged work elsewhere survives.
func checkoutPaths(wt *git.Worktree, to plumbing.Hash, paths map[string]bool) error {
	if len(paths) == 0 {
		// an empty Files list would make the hard reset cover everything
		return wt.Reset(&git.ResetOptions{Commit: to, Mode: git.SoftReset})
	}
	return wt.Reset(&git.ResetOptions{Commit: to, Mode: git.HardReset, Files: sortedKeys(paths)})
}

// uncommittedPaths lists tracked paths with work the operator has not committed: index
// entries that differ from HEAD (staged edits, additions, removals — compared directly,
// since go-git's Status reports a path removed from the index but still on disk as
// untracked) and tracked files edited in the worktree.
func uncommittedPaths(repo *git.Repository, wt *git.Worktree, head map[string]object.TreeEntry) (map[string]bool, error) {
	out := map[string]bool{}
	idx, err := repo.Storer.Index()
	if err != nil {
		return nil, fmt.Errorf("reading index: %w", err)
	}
	inIndex := make(map[string]bool, len(idx.Entries))
	for _, e := range idx.Entries {
		inIndex[e.Name] = true
		if h, ok := head[e.Name]; !ok || h.Hash != e.Hash || h.Mode != e.Mode {
			out[e.Name] = true
		}
	}
	for p := range head {
		if !inIndex[p] {
			out[p] = true
		}
	}
	st, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("reading worktree status: %w", err)
	}
	for p, s := range st {
		if s.Worktree != git.Unmodified && s.Worktree != git.Untracked {
			out[p] = true
		}
	}
	return out, nil
}

// operatorCollisions lists files the operator owns that checking out the given paths
// would overwrite or delete, given the original tree `head`:
//   - a file at a path that is not tracked in head (ignored files are expendable, as
//     in git);
//   - any file not tracked in head inside a directory a path replaces, or a file
//     sitting where a path needs a directory (go-git cannot clear either, so ignored
//     files count here);
//   - an unreadable path in the way, which cannot be checked.
func operatorCollisions(repo *git.Repository, repoRoot string, head map[string]object.TreeEntry, paths map[string]bool) ([]string, error) {
	matcher, err := ignoreMatcher(repo, repoRoot)
	if err != nil {
		return nil, err
	}
	tracked := func(p string) bool { _, ok := head[p]; return ok }

	found := map[string]bool{}
	for p := range paths {
		if e, ok := head[p]; ok && e.Mode == filemode.Submodule {
			continue // a submodule pointer: checkout updates the gitlink, not its files
		}
		full := filepath.Join(repoRoot, filepath.FromSlash(p))
		fi, err := os.Lstat(full)
		switch {
		case err == nil && fi.IsDir() && submoduleAt(full):
			// an initialised submodule the target now tracks: checkout leaves its files
		case err == nil && fi.IsDir():
			_ = filepath.WalkDir(full, func(q string, d fs.DirEntry, werr error) error {
				rel, _ := filepath.Rel(repoRoot, q)
				rel = filepath.ToSlash(rel)
				if werr != nil {
					found[rel] = true
					return nil
				}
				if !d.IsDir() && !tracked(rel) {
					found[rel] = true
				}
				return nil
			})
		case err == nil:
			if link := untrackedSymlinkAncestor(repoRoot, p, tracked); link != "" {
				found[link] = true // the checkout would replace the link with a directory
			} else if !tracked(p) && !matcher.Match(strings.Split(p, "/"), false) {
				found[p] = true
			}
		case !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR):
			found[p] = true
		default:
			for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
				fi, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(dir)))
				if err != nil {
					continue
				}
				if !fi.IsDir() && !tracked(dir) {
					found[dir] = true
				}
				break
			}
		}
	}
	return sortedKeys(found), nil
}

// ignoreMatcher matches what git ignores here. Patterns go in git's increasing
// priority: the one core.excludesFile in effect (repo config, else global, else system,
// else git's default $XDG_CONFIG_HOME/git/ignore), then .git/info/exclude and the
// .gitignore files, which win.
func ignoreMatcher(repo *git.Repository, repoRoot string) (gitignore.Matcher, error) {
	var patterns []gitignore.Pattern
	root := osfs.New("/")
	excludesFile := ""
	if cfg, err := repo.Config(); err == nil && cfg.Raw != nil {
		excludesFile = cfg.Raw.Section("core").Option("excludesfile")
	}
	if excludesFile != "" {
		patterns = readIgnoreFile(expandHome(excludesFile))
	} else if global, err := gitignore.LoadGlobalPatterns(root); err == nil && len(global) > 0 {
		patterns = global
	} else if system, err := gitignore.LoadSystemPatterns(root); err == nil && len(system) > 0 {
		patterns = system
	} else {
		patterns = readIgnoreFile(xdgGitIgnore())
	}
	repoPatterns, err := gitignore.ReadPatterns(osfs.New(repoRoot), nil)
	if err != nil {
		return nil, fmt.Errorf("reading ignore patterns: %w", err)
	}
	return gitignore.NewMatcher(append(patterns, repoPatterns...)), nil
}

// readIgnoreFile parses an ignore file; a missing or unreadable file adds nothing.
func readIgnoreFile(path string) []gitignore.Pattern {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []gitignore.Pattern
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimRight(line, "\r"); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, gitignore.ParsePattern(line, nil))
		}
	}
	return out
}

// xdgGitIgnore is git's default global ignore file.
func xdgGitIgnore() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "ignore")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "git", "ignore")
	}
	return ""
}

// expandHome resolves a leading ~/ as git does for core.excludesFile.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// untrackedSymlinkAncestor returns the nearest parent directory of p that is a symlink
// git does not track, or "".
func untrackedSymlinkAncestor(repoRoot, p string, tracked func(string) bool) string {
	for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
		fi, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(dir)))
		if err == nil && fi.Mode()&os.ModeSymlink != 0 && !tracked(dir) {
			return dir
		}
	}
	return ""
}

// submoduleAt reports a directory that is a checked-out submodule (it holds .git).
func submoduleAt(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// parentPath is the slash-separated parent of p, or "" at the top.
func parentPath(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return ""
}
