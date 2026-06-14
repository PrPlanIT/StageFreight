package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// cfgWithForge builds a minimal config with one forge and one primary repo —
// the shape resolveForgeProvider reads via config.ResolvePrimary.
func cfgWithForge(provider, url string) *config.Config {
	return &config.Config{
		Forges: []config.ForgeConfig{{ID: "f", Provider: provider, URL: url}},
		Repos:  []config.RepoConfig{{ID: "primary", Forge: "f", Roles: []string{"primary"}}},
	}
}

// TestResolveForgeProvider locks in the authority order: a DECLARED provider wins
// over the URL heuristic, so forge-backend pushes work behind reverse proxies, SSH
// aliases, and IP-based remotes the heuristic can't classify. Guards against any
// future reintroduction of URL-coupled provider selection.
func TestResolveForgeProvider(t *testing.T) {
	const ipRemote = "ssh://git@10.30.1.123:2424/SoFMeRight/dungeon.git"

	tests := []struct {
		name         string
		remoteURL    string
		cfg          *config.Config
		wantProvider forge.Provider
		wantURL      string
	}{
		{
			// The regression: declared provider:gitlab must win over an IP remote the
			// heuristic returns Unknown for. This is exactly the dungeon case.
			name:         "configured provider overrides unclassifiable IP remote",
			remoteURL:    ipRemote,
			cfg:          cfgWithForge("gitlab", "https://gitlab.prplanit.com"),
			wantProvider: forge.GitLab,
			wantURL:      "https://gitlab.prplanit.com", // configured API endpoint, not the IP
		},
		{
			name:         "no config falls back to URL heuristic",
			remoteURL:    "ssh://git@gitlab.example.com/g/r.git",
			cfg:          nil,
			wantProvider: forge.GitLab,
			wantURL:      "ssh://git@gitlab.example.com/g/r.git",
		},
		{
			name:         "no config plus opaque IP remote is Unknown",
			remoteURL:    ipRemote,
			cfg:          nil,
			wantProvider: forge.Unknown,
			wantURL:      ipRemote,
		},
		{
			name:         "invalid configured provider falls back to URL heuristic",
			remoteURL:    "https://github.com/o/r.git",
			cfg:          cfgWithForge("bogus", "https://example.com"),
			wantProvider: forge.GitHub,
			wantURL:      "https://github.com/o/r.git",
		},
		{
			name:         "configured provider without a URL keeps the remote as API base",
			remoteURL:    ipRemote,
			cfg:          cfgWithForge("gitlab", ""),
			wantProvider: forge.GitLab,
			wantURL:      ipRemote,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotURL := resolveForgeProvider(tt.remoteURL, tt.cfg)
			if gotProvider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", gotProvider, tt.wantProvider)
			}
			if gotURL != tt.wantURL {
				t.Errorf("clientURL = %q, want %q", gotURL, tt.wantURL)
			}
		})
	}
}
