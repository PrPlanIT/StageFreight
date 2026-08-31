package governance

import (
	"strings"
	"testing"
)

// End-to-end: what a satellite actually receives. An unqualified reference gains the
// governance source; a reference that declared its own keeps it; and each is retained
// under the key its own resolver will ask for.
func TestDistribution_PerReferenceProvenance(t *testing.T) {
	loader := fakeLoader{
		"preset/commit.yml": "commit:\n  conventional: true\n",
		"preset/lint.yml":   "lint:\n  level: full\n",
	}
	gov := &GovernanceConfig{Profiles: ProfileList{{
		ID: "p1",
		Config: map[string]any{
			"version": 1,
			"forges":  map[string]any{"gitlab": map[string]any{"provider": "gitlab", "url": "https://gl.example.com", "credentials": "GITLAB"}},
			"commit":  map[string]any{"preset": "preset/commit.yml"},
			"lint":    map[string]any{"preset": "preset/lint.yml"},
		},
		Repos: ProfileCatalog{{ID: "r1", At: "Org/repo"}},
	}}}
	ps := PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gl.example.com", ProjectID: "Org/policy"}
	plans, err := PlanDistribution(gov, loader, nil, nil, ps, "Org/policy")
	if err != nil {
		t.Fatalf("PlanDistribution: %v", err)
	}
	var cfg string
	paths := map[string]bool{}
	for _, f := range plans[0].Files {
		paths[f.Path] = true
		if f.Path == ".stagefreight.yml" {
			cfg = string(f.Content)
		}
	}

	// The satellite is told where each preset comes from, rather than being handed a
	// copy and left to assume.
	for _, want := range []string{
		"preset: https://gl.example.com/Org/policy//preset/commit.yml",
		"preset: https://gl.example.com/Org/policy//preset/lint.yml",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("distributed config missing %q\n%s", want, cfg)
		}
	}

	// And each is retained where that reference's own resolver will look for it, so the
	// fallback works when the source is unreachable.
	for _, want := range []string{
		".stagefreight/preset-cache/https-gl.example.com/Org/policy/preset/commit.yml",
		".stagefreight/preset-cache/https-gl.example.com/Org/policy/preset/lint.yml",
	} {
		if !paths[want] {
			t.Errorf("preset not retained at %q", want)
		}
	}
}
