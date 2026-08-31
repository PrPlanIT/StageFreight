package gitstate

import (
	"fmt"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
)

// RemoteRefExists reports whether ref exists as a branch and/or a tag on the remote at
// url, via a go-git ls-remote (no clone, no shell-out — the git-ops invariant requires
// go-git here). Used to classify a bare preset ref (branch → tracked, tag → pinned).
func RemoteRefExists(url, ref string) (branch, tag bool, err error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "src", URLs: []string{url}})
	refs, err := rem.List(&git.ListOptions{})
	if err != nil {
		return false, false, fmt.Errorf("ls-remote %s: %w", url, err)
	}
	head := plumbing.NewBranchReferenceName(ref)
	tagName := plumbing.NewTagReferenceName(ref)
	for _, r := range refs {
		switch r.Name() {
		case head:
			branch = true
		case tagName:
			tag = true
		}
	}
	return branch, tag, nil
}

// RemoteURL returns the URL for the given remote (typically "origin").
func RemoteURL(repo *git.Repository, remoteName string) (string, error) {
	cfg, err := repo.Config()
	if err != nil {
		return "", fmt.Errorf("reading repo config: %w", err)
	}
	r, ok := cfg.Remotes[remoteName]
	if !ok || len(r.URLs) == 0 {
		return "", fmt.Errorf("remote %q not configured", remoteName)
	}
	return r.URLs[0], nil
}

// RemoteRefHash returns the hash of a specific ref on the remote.
// Equivalent to `git ls-remote origin refs/heads/<branch>`.
// Requires network access.
func RemoteRefHash(repo *git.Repository, remoteName, refName string, auth transport.AuthMethod) (plumbing.Hash, error) {
	rem, err := repo.Remote(remoteName)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("opening remote %q: %w", remoteName, err)
	}

	refs, err := rem.List(&git.ListOptions{Auth: auth})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("listing remote refs: %w", err)
	}

	target := plumbing.NewBranchReferenceName(refName)
	for _, ref := range refs {
		if ref.Name() == target {
			return ref.Hash(), nil
		}
	}

	return plumbing.ZeroHash, fmt.Errorf("ref %q not found on remote %q", refName, remoteName)
}

// RemoteRefRevision returns the object id a remote ref currently points at, without
// fetching any content. Used to decide whether a tracked preset needs re-fetching at
// all, and to detect a pinned tag that has moved. ref "" resolves the remote's HEAD.
func RemoteRefRevision(url, ref string) (string, error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "src", URLs: []string{url}})
	refs, err := rem.List(&git.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("ls-remote %s: %w", url, err)
	}
	// A ref may be given bare ("main", "v1.0") or fully qualified; try the qualified
	// spellings first so a name that exists as both a branch and a tag resolves the
	// same way the fetch will.
	want := []plumbing.ReferenceName{
		plumbing.ReferenceName(ref),
		plumbing.NewBranchReferenceName(ref),
		plumbing.NewTagReferenceName(ref),
	}
	for _, w := range want {
		if ref == "" {
			break
		}
		for _, r := range refs {
			if r.Name() == w {
				return r.Hash().String(), nil
			}
		}
	}
	if ref == "" {
		for _, r := range refs {
			if r.Name() == plumbing.HEAD {
				return r.Hash().String(), nil
			}
		}
	}
	return "", fmt.Errorf("ref %q not found on %s", ref, url)
}
