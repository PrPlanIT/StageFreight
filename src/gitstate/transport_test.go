package gitstate

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// clearGitCredEnv blanks every credential env the transport decision reads, so a value set
// in the real environment cannot leak into an assertion.
func clearGitCredEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SSH_PRIVATE_KEY", "STAGEFREIGHT_GIT_PASSWORD", "STAGEFREIGHT_GIT_USERNAME",
		"GITLAB_TOKEN", "GITHUB_TOKEN", "GITEA_TOKEN", "FORGEJO_TOKEN", "CI_JOB_TOKEN",
		"CI_SERVER_HOST", "GL_TOKEN", "GH_TOKEN", "EXAMPLE_TOKEN",
	} {
		t.Setenv(k, "")
	}
}

func cfgWithForges(forges ...config.ForgeConfig) *config.Config {
	return &config.Config{Forges: forges}
}

// TestResolveTransport pins the transport-authority decision under HOST-BOUND auth:
// system git is the default for repository-local workflows; embedded is chosen only when
// a credential is host-bound to the configured forge that owns the URL's host.
func TestResolveTransport(t *testing.T) {
	t.Run("ssh remote, no injected key → system git", func(t *testing.T) {
		clearGitCredEnv(t)
		dec, err := ResolveTransport("ssh://git@example.com:22/o/r.git", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != PreferSystemGit {
			t.Errorf("got %v, want PreferSystemGit", dec.Preference)
		}
	})

	t.Run("https remote, no token/forge → system git", func(t *testing.T) {
		clearGitCredEnv(t)
		dec, err := ResolveTransport("https://example.com/o/r.git", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != PreferSystemGit {
			t.Errorf("got %v, want PreferSystemGit", dec.Preference)
		}
	})

	t.Run("https remote, configured forge + its token → embedded with auth", func(t *testing.T) {
		clearGitCredEnv(t)
		t.Setenv("EXAMPLE_TOKEN", "glpat-test")
		cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://example.com", Credentials: "EXAMPLE"})
		dec, err := ResolveTransport("https://example.com/o/r.git", cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != RequireEmbeddedTransport {
			t.Errorf("got %v, want RequireEmbeddedTransport", dec.Preference)
		}
		if dec.Auth == nil {
			t.Error("embedded decision must carry resolved auth")
		}
	})

	// SECURITY: a token in the environment whose host NO configured forge owns must NOT
	// trigger embedded transport — the token is never offered to that host.
	t.Run("https remote, token present but host unclaimed → system git (no injection)", func(t *testing.T) {
		clearGitCredEnv(t)
		t.Setenv("GITHUB_TOKEN", "ghp-should-not-be-sent-anywhere")
		cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://gitlab.example.com", Credentials: "GL"})
		dec, err := ResolveTransport("https://unclaimed.example.com/o/r.git", cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != PreferSystemGit {
			t.Errorf("an unclaimed host must not inject a credential; got %v", dec.Preference)
		}
	})
}

// TestSelectTransport asserts the selection maps a decision to the right implementation.
func TestSelectTransport(t *testing.T) {
	emb := selectTransport(nil, "/tmp/x", TransportDecision{Preference: RequireEmbeddedTransport})
	if _, ok := emb.(*embeddedTransport); !ok {
		t.Errorf("RequireEmbeddedTransport → %T, want *embeddedTransport", emb)
	}
	if gitAvailable() {
		sys := selectTransport(nil, "/tmp/x", TransportDecision{Preference: PreferSystemGit})
		if _, ok := sys.(*systemTransport); !ok {
			t.Errorf("PreferSystemGit (git present) → %T, want *systemTransport", sys)
		}
	}
}

// TestInjectedCredential pins the embedded-trigger signal under host-binding: an SSH key,
// or an HTTPS credential that the OWNING forge yields for the URL's host.
func TestInjectedCredential(t *testing.T) {
	t.Run("ssh + SSH_PRIVATE_KEY → injected", func(t *testing.T) {
		clearGitCredEnv(t)
		t.Setenv("SSH_PRIVATE_KEY", "-----BEGIN KEY-----")
		if !injectedCredential("ssh://git@host/o/r.git", nil) {
			t.Error("SSH_PRIVATE_KEY must count as injected")
		}
	})
	t.Run("ssh + agent/on-disk only → not injected", func(t *testing.T) {
		clearGitCredEnv(t)
		if injectedCredential("ssh://git@host/o/r.git", nil) {
			t.Error("an agent or on-disk key is the user's environment, not an injection")
		}
	})
	t.Run("https + owning forge token → injected", func(t *testing.T) {
		clearGitCredEnv(t)
		t.Setenv("GL_TOKEN", "glpat-x")
		cfg := cfgWithForges(config.ForgeConfig{Provider: "gitlab", URL: "https://host", Credentials: "GL"})
		if !injectedCredential("https://host/o/r.git", cfg) {
			t.Error("the owning forge's token must count as injected")
		}
	})
	t.Run("https + token but no owning forge → not injected", func(t *testing.T) {
		clearGitCredEnv(t)
		t.Setenv("GITHUB_TOKEN", "ghp-x")
		if injectedCredential("https://host/o/r.git", nil) {
			t.Error("a token with no owning forge must not count as injected")
		}
	})
}

// TestResolveTransportGitless verifies the other embedded trigger: with no git binary to
// delegate to, the decision falls to embedded even absent an injected credential.
func TestResolveTransportGitless(t *testing.T) {
	clearGitCredEnv(t)
	t.Setenv("PATH", "") // no git on PATH
	dec, err := ResolveTransport("https://host/o/r.git", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Preference != RequireEmbeddedTransport {
		t.Errorf("git-less → %v, want RequireEmbeddedTransport", dec.Preference)
	}
	// ...and with no owning forge, the embedded transport carries ANONYMOUS auth — a token
	// in env is never fabricated onto an unclaimed host.
	t.Setenv("GITHUB_TOKEN", "ghp-x")
	dec, _ = ResolveTransport("https://host/o/r.git", nil)
	if dec.Auth != nil {
		t.Errorf("git-less unclaimed host must carry nil auth, got %+v", dec.Auth)
	}
}
