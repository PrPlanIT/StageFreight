package gitstate

import (
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// clearGitCreds blanks every credential env the transport decision reads, so a value set
// in the real environment cannot leak into an assertion.
func clearGitCreds(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"STAGEFREIGHT_GIT_USERNAME", "STAGEFREIGHT_GIT_PASSWORD",
		"GITLAB_TOKEN", "GITHUB_TOKEN", "GITEA_TOKEN", "FORGEJO_TOKEN", "CI_JOB_TOKEN",
		"SSH_PRIVATE_KEY",
	} {
		t.Setenv(k, "")
	}
}

// A private HTTPS remote whose only credential is a forge-native token (Gitea/Forgejo)
// must resolve to the EMBEDDED transport carrying that credential — the path a freshness
// ls-remote and the deps write-back push both take. Before, these token names were
// unrecognized, so injectedCredential was false and the read/push fell to anonymous
// (fail-open freshness, failed push).
func TestResolveTransport_ForgeTokenSelectsEmbeddedAuth(t *testing.T) {
	for _, tc := range []struct{ env, user string }{
		{"GITEA_TOKEN", "git"},
		{"FORGEJO_TOKEN", "git"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			clearGitCreds(t)
			t.Setenv(tc.env, "forge-pat")
			dec, err := ResolveTransport("https://forge.example.com/o/r.git")
			if err != nil {
				t.Fatal(err)
			}
			if dec.Preference != RequireEmbeddedTransport {
				t.Fatalf("a forge token must force the embedded transport, got preference %v", dec.Preference)
			}
			ba, ok := dec.Auth.(*githttp.BasicAuth)
			if !ok {
				t.Fatalf("embedded auth = %T, want *githttp.BasicAuth", dec.Auth)
			}
			if ba.Username != tc.user || ba.Password != "forge-pat" {
				t.Errorf("embedded auth = %s:%s, want %s:forge-pat", ba.Username, ba.Password, tc.user)
			}
		})
	}
}

// With NO credential, the decision must never fabricate one: it is either system git
// (when a git binary exists) or embedded with nil (anonymous) auth — the genuine fallback
// that lets the freshness gate fail-open on an unauthenticatable private remote.
func TestResolveTransport_NoToken_NeverFabricatesCredential(t *testing.T) {
	clearGitCreds(t)
	dec, err := ResolveTransport("https://private.example.com/o/r.git")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Preference == RequireEmbeddedTransport && dec.Auth != nil {
		t.Errorf("no credential present, yet one was fabricated: %+v", dec.Auth)
	}
}
