package gitstate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
)

// systemTransport delegates Git transport to the system git binary. Git owns
// transport selection and authentication — ~/.ssh/config host/IdentityFile
// mappings, credential helpers, agents, SSH certificates, ProxyJump, Include
// directives, enterprise auth — none of which StageFreight reimplements. git's
// stdout/stderr are surfaced directly so the user sees real progress and errors
// (including auth prompts) exactly as a bare `git push` would.
type systemTransport struct {
	rootDir string
}

// Fetch mirrors the embedded fetch: branch heads only into remote-tracking refs,
// via an explicit refspec (not the user's fetch config) so no implicit tags or
// other namespaces are pulled — identical scope to the embedded path.
func (t *systemTransport) Fetch(remote string) error {
	return t.run("fetch", remote, fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", remote))
}

// FastForward fetches and fast-forward-only integrates the tracked upstream — the
// system equivalent of go-git's Worktree.Pull restricted to fast-forwards.
func (t *systemTransport) FastForward(remote string) error {
	return t.run("pull", "--ff-only", remote)
}

// Push pushes refspec to remote (empty refspec → git's default push behavior).
func (t *systemTransport) Push(remote, refspec string) error {
	args := []string{"push", remote}
	if refspec != "" {
		args = append(args, refspec)
	}
	return t.run(args...)
}

// RemoteRefHash resolves a branch head on the remote via `git ls-remote` — the
// system-git equivalent of the embedded go-git read, so the same auth applies as
// for fetch/push. Captures output rather than streaming it.
func (t *systemTransport) RemoteRefHash(remote, ref string) (plumbing.Hash, error) {
	out, err := exec.Command("git", "-C", t.rootDir, "ls-remote", remote, "refs/heads/"+ref).Output()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git ls-remote %s refs/heads/%s: %w", remote, ref, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) >= 1 && fields[0] != "" {
			return plumbing.NewHash(fields[0]), nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("ref %q not found on remote %q", ref, remote)
}

func (t *systemTransport) run(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", t.rootDir}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
