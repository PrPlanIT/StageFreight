package gitstate

import (
	"fmt"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// SyncSession is opened once per sync/push operation. State is resolved once at
// Open() and explicitly refreshed only after state-changing operations (Fetch,
// FastForward). Remote polling never happens opportunistically.
//
// The network surface (Fetch, FastForward, Push, RemoteRefHash) is delegated to a
// Transport chosen once at Open(): the system git binary for repository-local
// workflows, or in-process go-git when StageFreight holds an explicit credential
// or no git is available. A single session therefore never mixes transports, and
// the credential never escapes the transport boundary.
type SyncSession struct {
	repo      *git.Repository
	rootDir   string
	state     RepoState
	transport Transport
	fetched   bool // true after Fetch() invalidates ahead/behind
}

// OpenSyncSession opens a SyncSession for the repository at rootDir. Reads repo
// state and resolves the transport authority once; both are reused throughout the
// session. The transport decision is centralized in ResolveTransport — and when it
// selects system git, no go-git credential is resolved (so none can fail with the
// wrong key), because Git owns authentication.
func OpenSyncSession(rootDir string) (*SyncSession, error) {
	repo, err := OpenRepo(rootDir)
	if err != nil {
		return nil, fmt.Errorf("opening repo at %s: %w", rootDir, err)
	}

	state, err := ReadRepoState(repo)
	if err != nil {
		return nil, fmt.Errorf("reading repo state: %w", err)
	}
	// First-class git citizen: recognize an in-progress git op (merge/rebase/…) so the
	// planner can refuse with guidance instead of acting on a half-finished state.
	state.InProgressOp = DetectInProgressOp(rootDir)

	// Resolve the transport for the effective remote. When no upstream is
	// configured (first push), RemoteName is empty — fall back to "origin" so the
	// decision is always made against a concrete remote.
	effectiveRemote := state.RemoteName
	if effectiveRemote == "" {
		effectiveRemote = "origin"
	}
	remoteURL, remoteErr := RemoteURL(repo, effectiveRemote)
	if remoteErr != nil {
		return nil, fmt.Errorf("resolving remote URL for %s: %w", effectiveRemote, remoteErr)
	}

	dec, err := ResolveTransport(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("resolving transport for %s: %w", remoteURL, err)
	}

	return &SyncSession{
		repo:      repo,
		rootDir:   rootDir,
		state:     state,
		transport: selectTransport(repo, rootDir, dec),
	}, nil
}

// State returns the current resolved repo state.
func (s *SyncSession) State() RepoState {
	return s.state
}

// Repo returns the underlying git.Repository for local (non-transport) reads.
func (s *SyncSession) Repo() *git.Repository {
	return s.repo
}

// Refresh re-reads repo state after a mutation (fetch, fast-forward, reset).
func (s *SyncSession) Refresh() error {
	state, err := ReadRepoState(s.repo)
	if err != nil {
		return fmt.Errorf("refreshing repo state: %w", err)
	}
	s.state = state
	return nil
}

// Fetch fetches branch heads from the remote via the session transport, then
// refreshes state.
func (s *SyncSession) Fetch(remote string) error {
	if err := s.transport.Fetch(remote); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	s.fetched = true
	return s.Refresh()
}

// FastForward fast-forwards the tracked upstream via the session transport. The
// commit engine only reaches it from CLEAN_BEHIND (a state guard), so a
// non-fast-forward case is unreachable here — the embedded and system transports
// are therefore semantically equivalent even though their error types differ
// (no caller matches the typed go-git ErrNonFastForwardUpdate).
func (s *SyncSession) FastForward(remote string) error {
	if err := s.transport.FastForward(remote); err != nil {
		return err
	}
	return s.Refresh()
}

// Push pushes to remote via the session transport. When setUpstream is true it
// also configures branch tracking in .git/config — a local write, transport-
// agnostic, so it applies under both system git and embedded transport.
func (s *SyncSession) Push(remote, refspec string, setUpstream bool) error {
	if err := s.transport.Push(remote, refspec); err != nil {
		return err
	}
	if setUpstream && s.state.Branch != "" {
		_ = s.configureUpstream(remote, s.state.Branch)
	}
	return nil
}

// RemoteRefHash resolves a branch head on the remote through the session
// transport (system git ls-remote, or embedded go-git). Keeping remote reads
// behind the boundary is why no credential accessor leaks out of the session.
func (s *SyncSession) RemoteRefHash(remote, ref string) (plumbing.Hash, error) {
	return s.transport.RemoteRefHash(remote, ref)
}

// configureUpstream sets the upstream tracking branch in .git/config.
func (s *SyncSession) configureUpstream(remote, branch string) error {
	cfg, err := s.repo.Config()
	if err != nil {
		return err
	}
	if cfg.Branches == nil {
		cfg.Branches = make(map[string]*gitconfig.Branch)
	}
	cfg.Branches[branch] = &gitconfig.Branch{
		Name:   branch,
		Remote: remote,
		Merge:  plumbing.ReferenceName("refs/heads/" + branch),
	}
	return s.repo.SetConfig(cfg)
}

// FetchedUpstreamHash returns the upstream hash as observed after the last Fetch.
// Used by the replay race guard to detect concurrent pushes.
func (s *SyncSession) FetchedUpstreamHash() plumbing.Hash {
	return s.state.UpstreamHash
}
