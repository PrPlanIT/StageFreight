package gitstate

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
)

// TargetFacts describes where local HEAD sits relative to an EXPLICIT push destination
// (remote/branch) — e.g. pushing a feature branch to origin/main. Unlike RepoState's
// ahead/behind (always vs the branch's own upstream), these are computed against an
// arbitrary destination ref.
type TargetFacts struct {
	Exists bool // does refs/remotes/<remote>/<branch> exist?
	Ahead  int  // commits HEAD has that the destination lacks
	Behind int  // commits the destination has that HEAD lacks
}

// ResolveTargetFacts computes HEAD's position relative to an explicit destination. The
// caller must have fetched the remote first so the remote-tracking ref is current; this
// function performs no I/O beyond local ref/commit reads.
func ResolveTargetFacts(session *SyncSession, remote, branch string) (TargetFacts, error) {
	repo := session.repo
	headHash := session.state.HeadHash
	if headHash.IsZero() {
		return TargetFacts{}, fmt.Errorf("no HEAD commit to compare against %s/%s", remote, branch)
	}
	ref, err := repo.Reference(plumbing.NewRemoteReferenceName(remote, branch), true)
	if err != nil {
		// No remote-tracking ref for the destination → it does not exist yet (a new branch).
		return TargetFacts{Exists: false}, nil
	}
	ahead, behind, err := countAheadBehind(repo, headHash, ref.Hash())
	if err != nil {
		return TargetFacts{}, fmt.Errorf("computing ahead/behind vs %s/%s: %w", remote, branch, err)
	}
	return TargetFacts{Exists: true, Ahead: ahead, Behind: behind}, nil
}
