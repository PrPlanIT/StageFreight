package gitstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/kevinburke/ssh_config"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	sfxssh "github.com/PrPlanIT/StageFreight/src/ssh"
)

// isSSHURL returns true when the remote URL uses an SSH transport.
// Explicit match only — correctness over coverage.
func isSSHURL(url string) bool {
	ep, err := transport.NewEndpoint(url)
	return err == nil && ep.Protocol == "ssh"
}

// IsSSHURL is the exported form of isSSHURL for use by other packages.
func IsSSHURL(url string) bool { return isSSHURL(url) }

// ResolveAuth resolves the go-git SSH transport auth method for a remote URL.
//
// Resolution order (exclusive — first match wins):
//  1. SSH_PRIVATE_KEY env var (in-memory, no filesystem dependency)
//  2. SSH agent (SSH_AUTH_SOCK)
//  3. ~/.ssh/config IdentityFile for the host, then id_ed25519, id_ecdsa, id_rsa
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

	// Priority 3: ~/.ssh/config IdentityFile, then the standard key files — try each,
	// track last parse error.
	var lastKeyErr error
	for _, keyPath := range sshIdentityFiles(remoteURL) {
		if _, err := os.Stat(keyPath); err != nil {
			continue // file absent — not an error
		}
		signer, err := sfxssh.SignerFromFile(keyPath)
		if err != nil {
			lastKeyErr = fmt.Errorf("%s: %w", filepath.Base(keyPath), err)
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

// PushWillTriggerPipeline reports whether a git push to remoteURL will cause the forge to
// start a NEW pipeline on the pushed commit. The handoff that republishes a committed
// change — a dependency/base bump, an upstream sync — depends on this being true: the
// committing run defers the build to the pipeline that commit triggers.
//
// GitLab does NOT start a pipeline for a push authenticated with the CI job token
// (loop-prevention), so a bump pushed that way lands in the branch and never republishes.
// We detect exactly that: provider gitlab, no configured PAT/project-token for the forge,
// and the host-bound CI job token in play. The caller surfaces the gap loudly rather than
// assuming the handoff fired. Reason is non-empty only when the result is false.
//
// Every other case returns true: a configured token (PAT/project/group access token)
// triggers normally. NOTE scope: only GitLab's job-token non-trigger is detected today.
// GitHub has an analogous rule (a push authed with GITHUB_TOKEN does not start a workflow);
// detecting that needs different signals and is not yet covered, so a GitHub job-token push
// still returns true here — a known gap to close, not a claim it always triggers.
func PushWillTriggerPipeline(remoteURL string, cfg *config.Config) (bool, string) {
	if cfg == nil {
		return true, ""
	}
	forge := config.ForgeForURL(remoteURL, cfg.Forges, cfg.Vars)
	if forge == nil || forge.Provider != "gitlab" {
		return true, ""
	}
	if credentials.ResolvePrefix(forge.Credentials).Secret != "" {
		return true, "" // a real token (PAT/project/group) triggers a pipeline on push
	}
	if gitlabCIJobToken(config.HostOf(remoteURL)) != "" {
		return false, "pushed with the GitLab CI job token, which does not start a new pipeline — the commit will not build or republish until the forge credential is a project/group access token"
	}
	return true, ""
}

// sshUser is the SSH username for a remote: the URL's own, else ~/.ssh/config's User
// for its host, else "git".
func sshUser(remoteURL string) string {
	ep, err := transport.NewEndpoint(remoteURL)
	if err != nil {
		return "git"
	}
	if ep.User != "" {
		return ep.User
	}
	if u := sshConfig.Get(ep.Host, "User"); u != "" {
		return u
	}
	return "git"
}

// sshConfigReader is the part of ssh_config's settings this package reads.
type sshConfigReader interface {
	Get(alias, key string) string
	GetAll(alias, key string) []string
}

// sshConfig reads ~/.ssh/config and /etc/ssh/ssh_config, the files go-git's SSH
// transport already reads for Hostname and Port; a variable so tests can supply one.
var sshConfig sshConfigReader = ssh_config.DefaultUserSettings

// sshIdentityFiles are the keys to try for remoteURL: ~/.ssh/config's IdentityFile
// entries for its host, then the standard key files.
func sshIdentityFiles(remoteURL string) []string {
	home, _ := os.UserHomeDir()
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	if ep, err := transport.NewEndpoint(remoteURL); err == nil {
		for _, f := range sshConfig.GetAll(ep.Host, "IdentityFile") {
			add(f)
		}
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		add(filepath.Join(home, ".ssh", name))
	}
	return files
}
