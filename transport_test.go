package gitstate

import "testing"

// TestResolveTransport pins the centralized transport-authority decision: system
// git is the default for repository-local workflows; embedded is chosen only when
// StageFreight was handed an explicit credential to act as. The decision turns on
// "was a credential injected," never on environment inference (CI markers, etc.).
func TestResolveTransport(t *testing.T) {
	clearCredEnv := func(t *testing.T) {
		for _, k := range []string{
			"SSH_PRIVATE_KEY", "STAGEFREIGHT_GIT_PASSWORD", "STAGEFREIGHT_GIT_USERNAME",
			"GITLAB_TOKEN", "GITHUB_TOKEN", "CI_JOB_TOKEN",
		} {
			t.Setenv(k, "")
		}
	}

	t.Run("ssh remote, no injected key → system git (no go-git auth resolved)", func(t *testing.T) {
		clearCredEnv(t)
		dec, err := ResolveTransport("ssh://git@example.com:22/o/r.git")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != PreferSystemGit {
			t.Errorf("got %v, want PreferSystemGit", dec.Preference)
		}
	})

	t.Run("https remote, no token → system git", func(t *testing.T) {
		clearCredEnv(t)
		dec, err := ResolveTransport("https://example.com/o/r.git")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dec.Preference != PreferSystemGit {
			t.Errorf("got %v, want PreferSystemGit", dec.Preference)
		}
	})

	t.Run("https remote, injected token → embedded with auth", func(t *testing.T) {
		clearCredEnv(t)
		t.Setenv("GITLAB_TOKEN", "glpat-test")
		dec, err := ResolveTransport("https://example.com/o/r.git")
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
}

// TestSelectTransport asserts the selection maps a decision to the right
// implementation: embedded always yields go-git; system-preference yields system
// git when a git binary is available to delegate to.
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

// TestInjectedCredential pins the embedded-trigger signal: only a StageFreight-
// supplied credential (an in-memory SSH key, or an HTTP token) counts — an SSH
// agent or on-disk key is the user's environment, not an injection.
func TestInjectedCredential(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"SSH_PRIVATE_KEY", "STAGEFREIGHT_GIT_PASSWORD", "GITLAB_TOKEN", "GITHUB_TOKEN", "CI_JOB_TOKEN"} {
			t.Setenv(k, "")
		}
	}
	t.Run("ssh + SSH_PRIVATE_KEY → injected", func(t *testing.T) {
		clear(t)
		t.Setenv("SSH_PRIVATE_KEY", "-----BEGIN KEY-----")
		if !injectedCredential("ssh://git@host/o/r.git") {
			t.Error("SSH_PRIVATE_KEY must count as injected")
		}
	})
	t.Run("ssh + agent/on-disk only → not injected", func(t *testing.T) {
		clear(t)
		if injectedCredential("ssh://git@host/o/r.git") {
			t.Error("an agent or on-disk key is the user's environment, not an injection")
		}
	})
	t.Run("https + token → injected", func(t *testing.T) {
		clear(t)
		t.Setenv("GITLAB_TOKEN", "glpat-x")
		if !injectedCredential("https://host/o/r.git") {
			t.Error("HTTP token must count as injected")
		}
	})
	t.Run("https + nothing → not injected", func(t *testing.T) {
		clear(t)
		if injectedCredential("https://host/o/r.git") {
			t.Error("no token → not injected")
		}
	})
}

// TestResolveTransportGitless verifies the other embedded trigger: with no git
// binary to delegate to, the decision falls to embedded even absent an injected
// credential — proving git availability participates in the same decision model.
func TestResolveTransportGitless(t *testing.T) {
	for _, k := range []string{"SSH_PRIVATE_KEY", "STAGEFREIGHT_GIT_PASSWORD", "GITLAB_TOKEN", "GITHUB_TOKEN", "CI_JOB_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", "") // no git on PATH
	dec, err := ResolveTransport("https://host/o/r.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Preference != RequireEmbeddedTransport {
		t.Errorf("git-less → %v, want RequireEmbeddedTransport", dec.Preference)
	}
}
