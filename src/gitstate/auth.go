package gitstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport"

	sfxssh "github.com/PrPlanIT/StageFreight/src/ssh"
)

// isSSHURL returns true when the remote URL uses an SSH transport.
// Explicit match only — correctness over coverage.
func isSSHURL(url string) bool {
	return strings.HasPrefix(url, "ssh://") ||
		strings.HasPrefix(url, "git@")
}

// IsSSHURL is the exported form of isSSHURL for use by other packages.
func IsSSHURL(url string) bool { return isSSHURL(url) }

// ResolveAuth resolves the go-git SSH transport auth method for a remote URL.
//
// Resolution order (exclusive — first match wins):
//  1. SSH_PRIVATE_KEY env var (in-memory, no filesystem dependency)
//  2. SSH agent (SSH_AUTH_SOCK)
//  3. Standard key files: id_ed25519, id_ecdsa, id_rsa
//
// Host key verification is resolved via sfxssh.ResolveHostKeyCallback (same priority
// as raw SSH transport — SSH_KNOWN_HOSTS_CONTENT, SSH_KNOWN_HOSTS, ~/.ssh/known_hosts,
// SSH_INSECURE_SKIP_HOST_KEY_CHECK).
//
// Returns an error when no auth is available — SSH auth failure is always fatal.
func ResolveAuth(remoteURL string) (transport.AuthMethod, error) {
	user := sshUser(remoteURL)

	cb, err := sfxssh.ResolveHostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("resolving SSH host key callback: %w", err)
	}

	// Priority 1: SSH_PRIVATE_KEY env var — authoritative, skips agent and filesystem.
	if keyContent := os.Getenv("SSH_PRIVATE_KEY"); keyContent != "" {
		signer, err := sfxssh.SignerFromDataEnv([]byte(keyContent))
		if err != nil {
			return nil, fmt.Errorf("invalid SSH_PRIVATE_KEY: %w", err)
		}
		pkAuth := &gitssh.PublicKeys{User: user, Signer: signer}
		pkAuth.HostKeyCallback = cb
		return pkAuth, nil
	}

	// Priority 2: SSH agent.
	if os.Getenv("SSH_AUTH_SOCK") != "" {
		agentAuth, err := gitssh.NewSSHAgentAuth(user)
		if err == nil {
			agentAuth.HostKeyCallback = cb
			return agentAuth, nil
		}
		// Agent socket present but auth failed — continue to key files rather
		// than failing, but don't hide the reason. TODO: route through diag.Debug.
	}

	// Priority 3: standard key files — try each, track last parse error.
	home, _ := os.UserHomeDir()
	var lastKeyErr error
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		keyPath := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(keyPath); err != nil {
			continue // file absent — not an error
		}
		signer, err := sfxssh.SignerFromFile(keyPath)
		if err != nil {
			lastKeyErr = fmt.Errorf("%s: %w", name, err)
			continue // file present but unparseable — record and try next
		}
		pkAuth := &gitssh.PublicKeys{User: user, Signer: signer}
		pkAuth.HostKeyCallback = cb
		return pkAuth, nil
	}

	if lastKeyErr != nil {
		return nil, fmt.Errorf("SSH key found but could not be loaded: %w", lastKeyErr)
	}
	return nil, fmt.Errorf(
		"no SSH auth available for %s — set SSH_PRIVATE_KEY, SSH_AUTH_SOCK, "+
			"or place a key at ~/.ssh/{id_ed25519,id_ecdsa,id_rsa}",
		remoteURL,
	)
}

// ResolveHTTPAuth returns HTTP basic auth for an HTTPS remote, resolving a
// credential from the environment so CI write-back (e.g. the deps auto-commit
// push) authenticates instead of failing with "HTTP Basic: Access denied".
//
// Resolution order (first match wins):
//  1. STAGEFREIGHT_GIT_USERNAME + STAGEFREIGHT_GIT_PASSWORD — explicit override.
//  2. GITLAB_TOKEN — a Personal/Project Access Token (username "oauth2").
//  3. GITHUB_TOKEN — username "x-access-token".
//  4. CI_JOB_TOKEN — GitLab's per-job token (username "gitlab-ci-token"). LAST
//     resort: it is read-only for repository writes by default, so a push needs
//     a write-scoped token from (1)/(2); the job token only authenticates reads.
//
// Returns (nil, nil) when nothing is set — preserving anonymous access to public
// HTTPS repos. A nil return is not an error: SSH remotes never reach here, and an
// unauthenticated push to a private remote fails loudly at push time.
func ResolveHTTPAuth(_ string) (*githttp.BasicAuth, error) {
	if pass := os.Getenv("STAGEFREIGHT_GIT_PASSWORD"); pass != "" {
		user := os.Getenv("STAGEFREIGHT_GIT_USERNAME")
		if user == "" {
			user = "oauth2"
		}
		return &githttp.BasicAuth{Username: user, Password: pass}, nil
	}
	if tok := os.Getenv("GITLAB_TOKEN"); tok != "" {
		return &githttp.BasicAuth{Username: "oauth2", Password: tok}, nil
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return &githttp.BasicAuth{Username: "x-access-token", Password: tok}, nil
	}
	if tok := os.Getenv("CI_JOB_TOKEN"); tok != "" {
		return &githttp.BasicAuth{Username: "gitlab-ci-token", Password: tok}, nil
	}
	return nil, nil
}

// sshUser extracts the SSH username from a remote URL.
// git@host:path → "git", ssh://user@host:port/path → "user"
func sshUser(remoteURL string) string {
	if strings.HasPrefix(remoteURL, "ssh://") {
		rest := strings.TrimPrefix(remoteURL, "ssh://")
		if idx := strings.IndexByte(rest, '@'); idx > 0 {
			return rest[:idx]
		}
		return "git"
	}
	if idx := strings.IndexByte(remoteURL, '@'); idx > 0 {
		return remoteURL[:idx]
	}
	return "git"
}
