package config

import (
	"strings"
	"testing"
)

// TestValidateIdentityGraph_ForgeRegistryDisjoint verifies a forge and a registry may
// not share an id (the {path.<id>} namespace must be unambiguous), and that distinct
// ids raise no such error.
func TestValidateIdentityGraph_ForgeRegistryDisjoint(t *testing.T) {
	repos := []RepoConfig{{ID: "primary", Forge: "gitlab", Project: "o/r", Roles: []string{"primary"}, Branches: BranchesConfig{Default: "main"}}}

	// Shared id → disjoint error.
	forges := []ForgeConfig{{ID: "shared", Provider: "gitlab", URL: "https://x"}}
	registries := []RegistryConfig{{ID: "shared", Provider: "docker", URL: "docker.io"}}
	if !hasErrContaining(ValidateIdentityGraph(forges, repos, registries), "disjoint") {
		t.Errorf("expected a disjoint-id error when a forge and registry share id %q", "shared")
	}

	// Distinct ids → no disjoint error.
	forges2 := []ForgeConfig{{ID: "gitlab", Provider: "gitlab", URL: "https://x"}}
	registries2 := []RegistryConfig{{ID: "dockerhub", Provider: "docker", URL: "docker.io"}}
	if hasErrContaining(ValidateIdentityGraph(forges2, repos, registries2), "disjoint") {
		t.Error("distinct forge/registry ids should not raise a disjoint error")
	}
}

func hasErrContaining(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
