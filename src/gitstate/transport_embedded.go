package gitstate

import (
	"fmt"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gittransport "github.com/go-git/go-git/v5/plumbing/transport"
)

// embeddedTransport performs Git transport in-process via go-git with a
// StageFreight-supplied credential. It is selected only when StageFreight was
// explicitly handed a credential to act as (an injected SSH key or HTTP token),
// or when no git binary is available to delegate to. It deliberately does not
// reimplement ~/.ssh/config resolution — that path is reserved for explicit
// injected credentials, where there is nothing to resolve.
type embeddedTransport struct {
	repo *git.Repository
	auth gittransport.AuthMethod
}

func (t *embeddedTransport) Fetch(remote string) error {
	refspec := gitconfig.RefSpec(fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", remote))
	err := t.repo.Fetch(&git.FetchOptions{
		RemoteName: remote,
		RefSpecs:   []gitconfig.RefSpec{refspec},
		Auth:       t.auth,
	})
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	return nil
}

func (t *embeddedTransport) FastForward(remote string) error {
	wt, err := t.repo.Worktree()
	if err != nil {
		return fmt.Errorf("opening worktree: %w", err)
	}
	err = wt.Pull(&git.PullOptions{
		RemoteName: remote,
		Auth:       t.auth,
	})
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	if err != nil {
		return err // includes git.ErrNonFastForwardUpdate
	}
	return nil
}

func (t *embeddedTransport) Push(remote, refspec string) error {
	pushOpts := &git.PushOptions{
		RemoteName: remote,
		Auth:       t.auth,
	}
	if refspec != "" {
		pushOpts.RefSpecs = []gitconfig.RefSpec{gitconfig.RefSpec(refspec)}
	}
	err := t.repo.Push(pushOpts)
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	if err != nil {
		return fmt.Errorf("push to %s: %w", remote, err)
	}
	return nil
}

// RemoteRefHash resolves a branch head on the remote via go-git, carrying the
// injected credential. Delegates to the package-level RemoteRefHash helper.
func (t *embeddedTransport) RemoteRefHash(remote, ref string) (plumbing.Hash, error) {
	return RemoteRefHash(t.repo, remote, ref, t.auth)
}
