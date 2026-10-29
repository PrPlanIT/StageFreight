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

// Per-path outcomes a Policy can choose (the upstream.sync.files take: values).
const (
	Merge    = "merge"
	Fork     = "fork"
	Upstream = "upstream"
	Review   = "review"
)

// Policy names the outcome for a path when upstream changes it.
type Policy func(path string) string

// Result is a merged tree and what the merge decided along the way.
type Result struct {
	Tree       plumbing.Hash
	Conflicts  []string // paths that could not be joined; Tree is zero when any exist
	Held       []string // upstream changed these, the fork's version was kept (take: fork)
	Overridden []string // both changed, upstream's version replaced the fork's (take: upstream)
}

// Trees merges the fork's tree and upstream's against their common base, path by path. A path
// only one side changed takes that side — except that, when upstream is the one that changed
// it, the policy decides: fork keeps the fork's version (only when holdUntouched is set:
// a rebase holds those in a commit of its own), review makes it a conflict, anything else takes
// upstream's. A path both changed is merged line by line under merge; fork, upstream and review
// decide it outright. Binaries, mode changes and delete-against-edit only merge by rule.
func Trees(repo *git.Repository, base, fork, upstream *object.Tree, policy Policy, holdUntouched bool) (Result, error) {
	flat := func(t *object.Tree) (map[string]object.TreeEntry, error) {
		if t == nil {
			return map[string]object.TreeEntry{}, nil
		}
		return gitstate.FlattenTree(t)
	}
	b, err := flat(base)
	if err != nil {
		return Result{}, err
	}
	f, err := flat(fork)
	if err != nil {
		return Result{}, err
	}
	u, err := flat(upstream)
	if err != nil {
		return Result{}, err
	}
	if policy == nil {
		policy = func(string) string { return Merge }
	}

	paths := map[string]bool{}
	for _, m := range []map[string]object.TreeEntry{b, f, u} {
		for p := range m {
			paths[p] = true
		}
	}
	merged := map[string]object.TreeEntry{}
	var res Result
	same := func(x object.TreeEntry, inX bool, y object.TreeEntry, inY bool) bool {
		return inX == inY && (!inX || (x.Hash == y.Hash && x.Mode == y.Mode))
	}
	keep := func(p string, e object.TreeEntry, in bool) {
		if in {
			merged[p] = e
		}
	}
	for p := range paths {
		be, inB := b[p]
		fe, inF := f[p]
		ue, inU := u[p]
		switch {
		case same(fe, inF, ue, inU):
			keep(p, fe, inF)
		case same(ue, inU, be, inB): // only the fork changed it
			keep(p, fe, inF)
		case same(fe, inF, be, inB): // only upstream changed it
			switch policy(p) {
			case Fork:
				if holdUntouched {
					keep(p, fe, inF)
					res.Held = append(res.Held, p)
				} else {
					keep(p, ue, inU)
				}
			case Review:
				res.Conflicts = append(res.Conflicts, p)
			default:
				keep(p, ue, inU)
			}
		default: // both changed it
			switch policy(p) {
			case Fork:
				keep(p, fe, inF)
				res.Held = append(res.Held, p)
			case Upstream:
				keep(p, ue, inU)
				res.Overridden = append(res.Overridden, p)
			case Review:
				res.Conflicts = append(res.Conflicts, p)
			default:
				if !inF || !inU || fe.Mode != ue.Mode || (inB && be.Mode != fe.Mode) {
					res.Conflicts = append(res.Conflicts, p)
					continue
				}
				h, ok, err := mergeBlobs(repo, be, inB, fe, ue)
				if err != nil {
					return Result{}, fmt.Errorf("%s: %w", p, err)
				}
				if !ok {
					res.Conflicts = append(res.Conflicts, p)
					continue
				}
				merged[p] = object.TreeEntry{Name: p, Mode: fe.Mode, Hash: h}
			}
		}
	}
	sort.Strings(res.Conflicts)
	sort.Strings(res.Held)
	sort.Strings(res.Overridden)
	if len(res.Conflicts) > 0 {
		return res, nil
	}
	res.Tree, err = gitstate.WriteTreeFromFlat(repo, merged)
	return res, err
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
		// Scan the whole blob, not just the first 8000 bytes (git's diff sniff): this path
		// writes a merged blob, so a file that looks like text up front but is binary later
		// must be refused, never line-merged into a corrupted result.
		if bytes.IndexByte(data, 0) >= 0 {
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
