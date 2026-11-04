package gitstate

import (
	"github.com/go-git/go-git/v5/plumbing"
)

// Transport is the network-facing Git surface for a session, implemented in-process
// by go-git (embeddedTransport).
//
// Seam note: FastForward is, strictly, repository-sync policy rather than
// transport (it integrates fetched refs into the worktree). The honest future
// boundary is RemoteTransport{Fetch,Push,RemoteRefHash} + RepositorySync{FastForward}.
type Transport interface {
	Fetch(remote string) error
	FastForward(remote string) error
	Push(remote, refspec string) error
	RemoteRefHash(remote, ref string) (plumbing.Hash, error)
}
