package gitstate

import (
	"os/exec"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// Transport is the network-facing Git surface for a session. Two implementations
// exist: systemTransport delegates to the system git binary (the authority for
// repository-local workflows — it honors ~/.ssh/config, credential helpers,
// agents, certs, ProxyJump, Include, enterprise auth), and embeddedTransport runs
// in-process via go-git with a StageFreight-supplied credential. The whole surface
// is selected once per session so a single operation never mixes the two.
//
// Seam note: FastForward is, strictly, repository-sync policy rather than
// transport (it integrates fetched refs into the worktree). It lives here for now
// because both implementations must express it consistently; the honest future
// boundary is RemoteTransport{Fetch,Push,RemoteRefHash} + RepositorySync{FastForward}.
// RemoteRefHash, by contrast, is a genuine network read and belongs here.
type Transport interface {
	Fetch(remote string) error
	FastForward(remote string) error
	Push(remote, refspec string) error
	RemoteRefHash(remote, ref string) (plumbing.Hash, error)
}

// selectTransport maps a TransportDecision to its implementation. The decision —
// credentials AND git availability — is made once in ResolveTransport; selection
// here is a pure mapping that never re-derives the choice.
func selectTransport(repo *git.Repository, rootDir string, dec TransportDecision) Transport {
	if dec.Preference == RequireEmbeddedTransport {
		return &embeddedTransport{repo: repo, auth: dec.Auth}
	}
	return &systemTransport{rootDir: rootDir}
}

// gitAvailable reports whether a git binary is on PATH to delegate to. It feeds
// the transport decision in ResolveTransport — the single place the choice is made.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}
