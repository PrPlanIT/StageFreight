package commit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Two replay mechanisms share one verification vocabulary (changeSignature,
// ErrReplayCorruption) under different contracts:
//
//   - Replay (gogit_replay.go): WORKTREE mechanism — sequential user-commit
//     chains rebased via reset/apply/stage, gated by STRICT delta equality
//     (any divergence from the source delta is corruption).
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

	flat, err := flattenTree(tipTree)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("flattening tip tree: %w", err)
	}
	for _, ch := range sourceDelta {
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			delete(flat, ch.From.Name) // delete, or the departure side of a rename
		}
		if ch.To.Name != "" {
			flat[ch.To.Name] = object.TreeEntry{Name: ch.To.Name, Mode: ch.To.TreeEntry.Mode, Hash: ch.To.TreeEntry.Hash}
		}
	}

	newTreeHash, err := writeTreeFromFlat(repo, flat)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("writing replayed tree: %w", err)
	}

	newCommit := &object.Commit{
		Author:       localC.Author,
		Committer:    localC.Committer,
		Message:      localC.Message,
		TreeHash:     newTreeHash,
		ParentHashes: []plumbing.Hash{tip},
	}
	obj := repo.Storer.NewEncodedObject()
	if err := newCommit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encoding replayed commit: %w", err)
	}
	newHash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("storing replayed commit: %w", err)
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

// flattenTree maps every non-tree entry of a tree to its full path.
func flattenTree(t *object.Tree) (map[string]object.TreeEntry, error) {
	flat := map[string]object.TreeEntry{}
	type frame struct {
		prefix string
		tree   *object.Tree
	}
	stack := []frame{{"", t}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for i := range f.tree.Entries {
			e := f.tree.Entries[i]
			path := e.Name
			if f.prefix != "" {
				path = f.prefix + "/" + e.Name
			}
			if e.Mode == filemode.Dir {
				sub, err := f.tree.Tree(e.Name)
				if err != nil {
					return nil, fmt.Errorf("subtree %s: %w", path, err)
				}
				stack = append(stack, frame{path, sub})
				continue
			}
			flat[path] = object.TreeEntry{Name: path, Mode: e.Mode, Hash: e.Hash}
		}
	}
	return flat, nil
}

// writeTreeFromFlat materializes nested tree objects from a full-path entry map,
// bottom-up, in git's canonical tree order (a directory sorts as "name/").
func writeTreeFromFlat(repo *git.Repository, flat map[string]object.TreeEntry) (plumbing.Hash, error) {
	type dirNode struct {
		files map[string]object.TreeEntry
		dirs  map[string]*dirNode
	}
	newDir := func() *dirNode {
		return &dirNode{files: map[string]object.TreeEntry{}, dirs: map[string]*dirNode{}}
	}
	root := newDir()
	for path, entry := range flat {
		parts := strings.Split(path, "/")
		node := root
		for _, dir := range parts[:len(parts)-1] {
			child, ok := node.dirs[dir]
			if !ok {
				child = newDir()
				node.dirs[dir] = child
			}
			node = child
		}
		base := parts[len(parts)-1]
		node.files[base] = object.TreeEntry{Name: base, Mode: entry.Mode, Hash: entry.Hash}
	}

	var write func(n *dirNode) (plumbing.Hash, error)
	write = func(n *dirNode) (plumbing.Hash, error) {
		entries := make([]object.TreeEntry, 0, len(n.files)+len(n.dirs))
		for _, e := range n.files {
			entries = append(entries, e)
		}
		for name, child := range n.dirs {
			hash, err := write(child)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: hash})
		}
		sort.Slice(entries, func(i, j int) bool {
			return treeSortKey(entries[i]) < treeSortKey(entries[j])
		})
		tree := &object.Tree{Entries: entries}
		obj := repo.Storer.NewEncodedObject()
		if err := tree.Encode(obj); err != nil {
			return plumbing.ZeroHash, err
		}
		return repo.Storer.SetEncodedObject(obj)
	}
	return write(root)
}

// treeSortKey renders git's canonical tree ordering key: byte-wise by name,
// with directories comparing as if their name ended in "/".
func treeSortKey(e object.TreeEntry) string {
	if e.Mode == filemode.Dir {
		return e.Name + "/"
	}
	return e.Name
}

func short(h plumbing.Hash) string { return h.String()[:8] }
