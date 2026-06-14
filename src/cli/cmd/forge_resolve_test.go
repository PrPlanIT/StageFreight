package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// cfgPrimary builds a minimal config: one forge plus one primary repo on it —
// the shape forgeIdentityFromConfig reads via config.ResolvePrimary.
func cfgPrimary(provider, url, project string) *config.Config {
	return &config.Config{
		Forges: []config.ForgeConfig{{ID: "f", Provider: provider, URL: url, Credentials: "GITLAB"}},
		Repos:  []config.RepoConfig{{ID: "primary", Forge: "f", Project: project, Roles: []string{"primary"}}},
	}
}

// TestForgeIdentityFromConfig locks in that push-target identity comes from the
// resolved primary repo (provider + base URL + project) — config-driven, independent
// of the git remote and CI env. Regression guard for the dungeon case: a declared
// provider:gitlab + project resolve regardless of an IP/proxy/SSH-alias remote that the
// URL heuristic can't classify, and without needing a token (auth is a later concern).
func TestForgeIdentityFromConfig(t *testing.T) {
	t.Run("declared primary forge resolves full identity", func(t *testing.T) {
		got := forgeIdentityFromConfig(cfgPrimary("gitlab", "https://gitlab.prplanit.com", "SoFMeRight/dungeon"))
		if got == nil {
			t.Fatal("expected resolved identity, got nil")
		}
		if got.Provider != "gitlab" {
			t.Errorf("provider = %q, want gitlab", got.Provider)
		}
		if got.Project != "SoFMeRight/dungeon" {
			t.Errorf("project = %q, want SoFMeRight/dungeon", got.Project)
		}
		if got.BaseURL != "https://gitlab.prplanit.com" {
			t.Errorf("baseURL = %q, want https://gitlab.prplanit.com", got.BaseURL)
		}
	})

	t.Run("nil config falls back to remote heuristics (nil)", func(t *testing.T) {
		if got := forgeIdentityFromConfig(nil); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("unknown declared provider falls back (nil)", func(t *testing.T) {
		if got := forgeIdentityFromConfig(cfgPrimary("bogus", "https://x", "g/r")); got != nil {
			t.Errorf("expected nil for unknown provider, got %+v", got)
		}
	})

	t.Run("no primary repo falls back (nil)", func(t *testing.T) {
		cfg := &config.Config{
			Forges: []config.ForgeConfig{{ID: "f", Provider: "gitlab", URL: "https://x"}},
			Repos:  []config.RepoConfig{{ID: "m", Forge: "f", Roles: []string{"mirror"}}},
		}
		if got := forgeIdentityFromConfig(cfg); got != nil {
			t.Errorf("expected nil with no primary repo, got %+v", got)
		}
	})
}
