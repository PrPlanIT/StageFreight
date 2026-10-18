package gitstate

import (
	"fmt"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FlattenTree maps every non-tree entry of a tree to its full path.
func FlattenTree(t *object.Tree) (map[string]object.TreeEntry, error) {
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

// WriteTreeFromFlat materializes nested tree objects from a full-path entry map,
// bottom-up, in git's canonical tree order (a directory sorts as "name/").
func WriteTreeFromFlat(repo *git.Repository, flat map[string]object.TreeEntry) (plumbing.Hash, error) {
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
