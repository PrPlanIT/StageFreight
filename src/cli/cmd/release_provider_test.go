package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/registry"
)

// TestProviderFromHostTokensNeverPanicURLBuilders guards the v0.6.0 release
// regression: providerFromHost's result is assigned to
// registry.ResolvedRegistryTarget.Provider, and RepoURL/TagURL PANIC on an
// unrecognized provider. So every token the heuristic can emit MUST be one those
// builders handle. (docker.io once returned "dockerhub", which panicked TagURL
// during RunReleaseCreate and failed the release job.)
func TestProviderFromHostTokensNeverPanicURLBuilders(t *testing.T) {
	want := map[string]string{
		"docker.io":           "docker",
		"ghcr.io":             "github",
		"registry.gitlab.com": "gitlab",
		"gitea.example.com":   "gitea",
		"harbor.example.com":  "harbor",
		"team.jfrog.io":       "jfrog",
		"quay.io":             "quay",
		"cr.pcfae.com":        "generic", // host doesn't reveal it's Harbor → neutral fallback, not a crash
		"some.unknown.host":   "generic",
	}
	for host, prov := range want {
		got := providerFromHost(host)
		if got != prov {
			t.Errorf("providerFromHost(%q) = %q, want %q", host, got, prov)
		}
		rt := registry.ResolvedRegistryTarget{Provider: got, Host: host, Path: "org/app"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("URL builders panicked for host %q (provider %q): %v", host, rt.Provider, r)
				}
			}()
			_ = rt.TagURL("v1.0.0")
			_ = rt.RepoURL()
			_ = rt.DisplayName()
		}()
	}
}
