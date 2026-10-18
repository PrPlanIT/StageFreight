package merge

import (
	"bytes"
	"fmt"
	"io"
	"sort"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// Trees merges ours and theirs against base, file by file: a path changed on one side takes
// that side; a text file changed on both is merged line by line; anything else changed on
// both — a binary, a mode change, a delete against an edit, the same new path added twice
// differently — is a conflict. The merged tree is written to the repository. conflicts lists
// the conflicting paths; when it is non-empty the tree is not written and must not be used.
func Trees(repo *git.Repository, base, ours, theirs *object.Tree) (plumbing.Hash, []string, error) {
	flat := func(t *object.Tree) (map[string]object.TreeEntry, error) {
		if t == nil {
			return map[string]object.TreeEntry{}, nil
		}
		return gitstate.FlattenTree(t)
	}
	b, err := flat(base)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	o, err := flat(ours)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	th, err := flat(theirs)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}

	paths := map[string]bool{}
	for _, m := range []map[string]object.TreeEntry{b, o, th} {
		for p := range m {
			paths[p] = true
		}
	}
	merged := map[string]object.TreeEntry{}
	var conflicts []string
	for p := range paths {
		be, inB := b[p]
		oe, inO := o[p]
		te, inT := th[p]
		same := func(x object.TreeEntry, inX bool, y object.TreeEntry, inY bool) bool {
			return inX == inY && (!inX || (x.Hash == y.Hash && x.Mode == y.Mode))
		}
		switch {
		case same(oe, inO, te, inT):
			if inO {
				merged[p] = oe
			}
		case same(oe, inO, be, inB):
			if inT {
				merged[p] = te
			}
		case same(te, inT, be, inB):
			if inO {
				merged[p] = oe
			}
		case inO && inT && oe.Mode == te.Mode && (!inB || be.Mode == oe.Mode):
			h, ok, err := mergeBlobs(repo, be, inB, oe, te)
			if err != nil {
				return plumbing.ZeroHash, nil, fmt.Errorf("%s: %w", p, err)
			}
			if !ok {
				conflicts = append(conflicts, p)
				continue
			}
			merged[p] = object.TreeEntry{Name: p, Mode: oe.Mode, Hash: h}
		default:
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return plumbing.ZeroHash, conflicts, nil
	}
	h, err := gitstate.WriteTreeFromFlat(repo, merged)
	return h, nil, err
}

// mergeBlobs merges two text versions of a file against their base (empty when the file is
// new on both sides). A binary on any side is not merged.
func mergeBlobs(repo *git.Repository, be object.TreeEntry, inB bool, oe, te object.TreeEntry) (plumbing.Hash, bool, error) {
	read := func(h plumbing.Hash) ([]byte, error) {
		blob, err := repo.BlobObject(h)
		if err != nil {
			return nil, err
		}
		r, err := blob.Reader()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}
	var base []byte
	if inB {
		var err error
		if base, err = read(be.Hash); err != nil {
			return plumbing.ZeroHash, false, err
		}
	}
	ours, err := read(oe.Hash)
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	theirs, err := read(te.Hash)
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	for _, data := range [][]byte{base, ours, theirs} {
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return plumbing.ZeroHash, false, nil
		}
	}
	text, ok := Text(string(base), string(ours), string(theirs))
	if !ok {
		return plumbing.ZeroHash, false, nil
	}
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, false, err
	}
	if _, err := w.Write([]byte(text)); err != nil {
		return plumbing.ZeroHash, false, err
	}
	w.Close()
	h, err := repo.Storer.SetEncodedObject(obj)
	return h, err == nil, err
}
