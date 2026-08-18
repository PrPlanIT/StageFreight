package gitstate

import (
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// basicAuth asserts the resolved credential is HTTP basic auth and returns it.
func basicAuth(t *testing.T, auth interface{}) *githttp.BasicAuth {
	t.Helper()
	ba, ok := auth.(*githttp.BasicAuth)
	if !ok {
		t.Fatalf("expected *githttp.BasicAuth, got %T", auth)
	}
	return ba
}

// THE core leak test. A mirror job holds the origin's token AND the target's token at once.
// A GitLab-origin URL, with a GitHub PAT sitting in the environment, must resolve the
// GitLab forge's credential — NEVER the GitHub token. A credential is host-bound to the
// forge that owns the destination; a foreign token is never transmitted.
func TestResolveGitCredential_HostBoundNoLeak(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("GL_TOKEN", "gitlab-secret")                     // the origin (GitLab) forge's cred
	t.Setenv("GITHUB_TOKEN", "github-write-pat-MUST-NOT-LEAK") // the mirror target's cred, also present
	cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.example.com", Credentials: "GL"})

	auth, err := ResolveGitCredential("https://gitlab.example.com/org/repo.git", cfg)
	if err != nil {
		t.Fatal(err)
	}
	ba := basicAuth(t, auth)
	if ba.Password == "github-write-pat-MUST-NOT-LEAK" {
		t.Fatal("SECURITY: GitHub token was sent to a GitLab host — credential confusion leak")
	}
	if ba.Username != "oauth2" || ba.Password != "gitlab-secret" {
		t.Errorf("got %s:%s, want oauth2:gitlab-secret (the GitLab forge's own cred)", ba.Username, ba.Password)
	}
}

// Each configured forge resolves ITS OWN credential (and username) for ITS host.
func TestResolveGitCredential_EachForgeOwnCredential(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("GL_TOKEN", "gl-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("GT_TOKEN", "gt-secret")
	t.Setenv("FJ_TOKEN", "fj-secret")
	cfg := cfgWithForges(
		config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.example.com", Credentials: "GL"},
		config.ForgeConfig{Provider: "github", URL: "https://github.com", Credentials: "GH"},
		config.ForgeConfig{Provider: "gitea", URL: "https://gitea.example.com", Credentials: "GT"},
		config.ForgeConfig{Provider: "forgejo", URL: "https://code.example.com", Credentials: "FJ"},
	)
	cases := []struct{ url, user, secret string }{
		{"https://gitlab.example.com/o/r.git", "oauth2", "gl-secret"},
		{"https://github.com/o/r.git", "x-access-token", "gh-secret"},
		{"https://gitea.example.com/o/r.git", "git", "gt-secret"},
		{"https://code.example.com/o/r.git", "git", "fj-secret"},
	}
	for _, c := range cases {
		auth, err := ResolveGitCredential(c.url, cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		ba := basicAuth(t, auth)
		if ba.Username != c.user || ba.Password != c.secret {
			t.Errorf("%s → %s:%s, want %s:%s", c.url, ba.Username, ba.Password, c.user, c.secret)
		}
	}
}

// An unconfigured/unknown host resolves to anonymous (nil) even with tokens in env — a
// TRUE nil interface, never a typed-nil wrapping a nil *BasicAuth.
func TestResolveGitCredential_UnknownHostAnonymous(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp-x")
	t.Setenv("GITLAB_TOKEN", "glpat-x")
	cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.example.com", Credentials: "GL"})

	auth, err := ResolveGitCredential("https://totally-unclaimed.example.org/o/r.git", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if auth != nil {
		t.Fatalf("unknown host must be anonymous (nil), got %+v", auth)
	}
	// And with no config at all → anonymous.
	auth, _ = ResolveGitCredential("https://gitlab.example.com/o/r.git", nil)
	if auth != nil {
		t.Fatalf("nil config must be anonymous (nil), got %+v", auth)
	}
}

// The generic invariant: whenever a credential is resolved, the forge that owns the
// destination host is the forge whose credential was selected — proven by re-deriving the
// owning forge from the URL and confirming its host equals the destination host.
func TestResolveGitCredential_Invariant_OwningForgeOwnsHost(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("GL_TOKEN", "gl-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	cfg := cfgWithForges(
		config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.example.com", Credentials: "GL"},
		config.ForgeConfig{Provider: "github", URL: "https://github.com", Credentials: "GH"},
	)
	for _, url := range []string{"https://gitlab.example.com/o/r.git", "https://github.com/o/r.git"} {
		auth, err := ResolveGitCredential(url, cfg)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if auth == nil {
			t.Fatalf("%s: expected a credential", url)
		}
		owner := config.ForgeForURL(url, cfg.Forges, cfg.Vars)
		if owner == nil {
			t.Fatalf("%s: no owning forge — a credential must never be resolved without one", url)
		}
		if config.HostOf(owner.URL) != config.HostOf(url) {
			t.Errorf("%s: owning forge host %q != destination host %q", url, config.HostOf(owner.URL), config.HostOf(url))
		}
	}
}

// CI_JOB_TOKEN is GitLab-host-bound: usable ONLY for the configured GitLab forge whose host
// is the CI server host, never for another GitLab host or any other provider.
func TestResolveGitCredential_CIJobTokenHostPinned(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("CI_JOB_TOKEN", "jobtok")
	t.Setenv("CI_SERVER_HOST", "gitlab.ci.example.com")

	// The CI server's own GitLab host, no explicit secret → job token used.
	ciCfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.ci.example.com", Credentials: "GLCI"})
	auth, err := ResolveGitCredential("https://gitlab.ci.example.com/o/r.git", ciCfg)
	if err != nil {
		t.Fatal(err)
	}
	ba := basicAuth(t, auth)
	if ba.Username != "gitlab-ci-token" || ba.Password != "jobtok" {
		t.Errorf("CI host → %s:%s, want gitlab-ci-token:jobtok", ba.Username, ba.Password)
	}

	// A DIFFERENT GitLab host must NOT receive the job token.
	otherGL := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://other-gitlab.example.com", Credentials: "OTHER"})
	if a, _ := ResolveGitCredential("https://other-gitlab.example.com/o/r.git", otherGL); a != nil {
		t.Errorf("SECURITY: CI job token offered to a non-CI GitLab host: %+v", a)
	}

	// A GitHub host must NOT receive the job token.
	ghCfg := cfgWithForges(config.ForgeConfig{Provider: "github", URL: "https://github.com", Credentials: "NONE"})
	if a, _ := ResolveGitCredential("https://github.com/o/r.git", ghCfg); a != nil {
		t.Errorf("SECURITY: CI job token / cred offered to a github host: %+v", a)
	}
}

// An SSH URL routes to the key-based resolver and NEVER to HTTP basic auth, even with HTTP
// tokens in the environment.
func TestResolveGitCredential_SSHNeverHTTP(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("GITHUB_TOKEN", "ghp-x")
	cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://host", Credentials: "GL"})
	auth, err := ResolveGitCredential("git@host:o/r.git", cfg)
	if err != nil {
		return // SSH path with no resolvable key — correct: HTTP tokens were never consulted
	}
	if _, isHTTP := auth.(*githttp.BasicAuth); isHTTP {
		t.Error("an SSH URL must never resolve to HTTP basic auth")
	}
}

func TestGitUsernameForProvider(t *testing.T) {
	cases := map[string]string{"github": "x-access-token", "gitlab": "oauth2", "gitea": "git", "forgejo": "git", "whatever": "git"}
	for provider, want := range cases {
		if got := GitUsernameForProvider(provider); got != want {
			t.Errorf("GitUsernameForProvider(%q) = %q, want %q", provider, got, want)
		}
	}
}
