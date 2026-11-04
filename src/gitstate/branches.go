package gitstate

import (
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CurrentBranch returns the branch HEAD is on, or false when HEAD is detached.
func CurrentBranch(repo *git.Repository) (string, bool) {
	head, err := repo.Reference(plumbing.HEAD, false)
	if err != nil || head.Type() != plumbing.SymbolicReference || !head.Target().IsBranch() {
		return "", false
	}
	return head.Target().Short(), true
}

// BranchesContaining returns the names of the branches whose tip is the commit or descends
// from it: local branches by name, remote-tracking branches by the name on their remote
// (origin/main counts as main). Sorted and deduplicated.
func BranchesContaining(repo *git.Repository, commit plumbing.Hash) ([]string, error) {
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() != plumbing.HashReference {
			return nil
		}
		var name string
		switch {
		case ref.Name().IsBranch():
			name = ref.Name().Short()
		case ref.Name().IsRemote():
			_, branch, ok := strings.Cut(ref.Name().Short(), "/")
			if !ok || branch == "HEAD" {
				return nil
			}
			name = branch
		default:
			return nil
		}
		if seen[name] {
			return nil
		}
		ok, err := isAncestorHash(repo, commit, ref.Hash())
		if err != nil {
			return err
		}
		if ok {
			seen[name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// UpstreamBranch returns the remote branch name the local branch tracks, or false when it
// tracks none.
func UpstreamBranch(repo *git.Repository, branch string) (string, bool) {
	cfg, err := repo.Config()
	if err != nil {
		return "", false
	}
	b := cfg.Branches[branch]
	if b == nil || b.Merge == "" || !b.Merge.IsBranch() {
		return "", false
	}
	return b.Merge.Short(), true
}
