package gitstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
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

// GitUsernameForProvider is the HTTP basic-auth username a forge provider expects when the
// token is carried as the password. It is the single source of truth for this mapping,
// shared by the transport resolver and the mirror push.
func GitUsernameForProvider(provider string) string {
	switch provider {
	case "github":
		return "x-access-token"
	case "gitlab":
		return "oauth2"
	default:
		// gitea, forgejo, and any self-hosted git server: the token authenticates as
		// the "git" user, matching the mirror's long-standing resolveGitAuth default.
		return "git"
	}
}

// ResolveGitCredential resolves the credential for a git remote URL, HOST-BOUND. This is
// the security boundary against credential confusion: the destination host must resolve to
// a configured `forges:` entry, and THAT forge's declared credential is the ONLY one
// eligible. A credential is never selected merely because its env var is set — so a GitHub
// PAT can never become an auth attempt against a GitLab host, and vice-versa.
//
// Resolution is strictly: URL host → configured forge (exact host match) → forge.credentials
// → credential. SSH URLs delegate to ResolveAuth (the user's key env). For an HTTPS URL
// whose host no configured forge owns — or when no config is threaded — the result is
// anonymous (a true nil AuthMethod, never a typed-nil), i.e. FAIL-CLOSED: a token in the
// environment is never transmitted to an unclaimed host.
func ResolveGitCredential(remoteURL string, cfg *config.Config) (transport.AuthMethod, error) {
	if isSSHURL(remoteURL) {
		return ResolveAuth(remoteURL)
	}
	if cfg == nil {
		return nil, nil // no forge graph to bind against — anonymous, fail-closed
	}
	forge := config.ForgeForURL(remoteURL, cfg.Forges, cfg.Vars)
	if forge == nil {
		return nil, nil // no configured forge owns this host — anonymous, fail-closed
	}
	ba := gitBasicAuthForForge(*forge, config.HostOf(remoteURL))
	if ba == nil {
		return nil, nil // return a TRUE nil, not an interface wrapping a nil *BasicAuth
	}
	return ba, nil
}

// gitBasicAuthForForge builds the HTTP basic auth for one forge, or nil when the forge has
// no resolvable credential. destHost is the already-normalized destination host, used to
// pin the GitLab CI job token to the CI server host.
func gitBasicAuthForForge(forge config.ForgeConfig, destHost string) *githttp.BasicAuth {
	creds := credentials.ResolvePrefix(forge.Credentials)
	if creds.Secret == "" {
		// CI_JOB_TOKEN is a HOST-BOUND last resort, ONLY for the configured GitLab forge
		// whose host is the CI server host — never a generic fallback for github/gitea/
		// forgejo/unknown hosts. It authenticates reads for the GitLab instance running
		// this pipeline and nothing else.
		if forge.Provider == "gitlab" {
			if tok := gitlabCIJobToken(destHost); tok != "" {
				return &githttp.BasicAuth{Username: "gitlab-ci-token", Password: tok}
			}
		}
		return nil
	}
	user := creds.User
	if user == "" {
		user = GitUsernameForProvider(forge.Provider)
	}
	return &githttp.BasicAuth{Username: user, Password: creds.Secret}
}

// gitlabCIJobToken returns CI_JOB_TOKEN only when the destination host IS the GitLab CI
// server host (CI_SERVER_HOST). This pins the job token to the one GitLab instance that
// issued it; it is never offered to any other host.
func gitlabCIJobToken(destHost string) string {
	tok := os.Getenv("CI_JOB_TOKEN")
	if tok == "" || destHost == "" {
		return ""
	}
	if serverHost := config.HostOf(os.Getenv("CI_SERVER_HOST")); serverHost != "" && serverHost == destHost {
		return tok
	}
	return ""
}

// TransportPreference expresses who owns the Git transport for a remote. It is the
// centralized output of credential resolution — transport SELECTION consumes it
// and never re-scans the environment in another package.
type TransportPreference int

const (
	// PreferSystemGit delegates transport to the system git binary, the authority
	// for repository-local workflows: it already honors ~/.ssh/config, credential
	// helpers, agents, SSH certs, ProxyJump, Include, and enterprise auth that
	// StageFreight would otherwise have to reimplement — and be less capable than.
	PreferSystemGit TransportPreference = iota
	// RequireEmbeddedTransport uses in-process go-git with a StageFreight-supplied
	// credential. Chosen only when StageFreight was explicitly handed a credential
	// to act as, independent of the user's Git environment.
	RequireEmbeddedTransport
)

// TransportDecision is the centralized transport-authority decision: which
// transport to use and, when embedded, the resolved credential to use with it.
type TransportDecision struct {
	Preference TransportPreference
	Auth       transport.AuthMethod
}

// ResolveTransport decides who owns the Git transport for remoteURL. The question
// is not "where are we running" but "was StageFreight explicitly entrusted with a
// credential to act independently of the user's Git environment?" For an SSH remote
// that credential is SSH_PRIVATE_KEY; for HTTPS it is the HOST-BOUND credential of the
// configured forge that owns the URL's host (cfg). Absent an explicit credential, the
// repository's own Git is the transport authority (PreferSystemGit) — so credential
// helpers, config-mapped keys, agents, certs, and enterprise auth all work, because Git
// (not StageFreight) handles them.
//
// cfg carries the forge graph the credential is bound against; nil ⇒ no host-bound
// credential is available, so a private HTTPS remote resolves anonymously (fail-closed).
func ResolveTransport(remoteURL string, cfg *config.Config) (TransportDecision, error) {
	if gitAvailable() && !injectedCredential(remoteURL, cfg) {
		return TransportDecision{Preference: PreferSystemGit}, nil
	}
	// Embedded: StageFreight holds an explicit host-bound credential, or there is no git
	// to delegate to. Resolve the credential go-git will carry (host-bound, may be nil).
	auth, err := ResolveGitCredential(remoteURL, cfg)
	if err != nil {
		return TransportDecision{}, err
	}
	return TransportDecision{Preference: RequireEmbeddedTransport, Auth: auth}, nil
}

// injectedCredential reports whether StageFreight has a HOST-BOUND credential to act as
// for remoteURL — an in-memory SSH key for an SSH remote, or the configured owning forge's
// credential for an HTTPS remote. An SSH agent or on-disk key is NOT injected: it belongs
// to the user's Git environment, which system git uses directly. Deriving HTTP recognition
// from ResolveGitCredential keeps the transport decision and the credential the embedded
// transport will carry from ever drifting.
func injectedCredential(remoteURL string, cfg *config.Config) bool {
	if isSSHURL(remoteURL) {
		return os.Getenv("SSH_PRIVATE_KEY") != ""
	}
	auth, _ := ResolveGitCredential(remoteURL, cfg)
	return auth != nil
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
