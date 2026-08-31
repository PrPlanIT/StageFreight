package governance

import (
	"github.com/PrPlanIT/StageFreight/src/presetref"
	"strings"
	"testing"
)

// One profile naming every source family: governance-owned, arbitrary URL, another
// forge tracked, another forge pinned by tag, and pinned by sha. Each must survive
// distribution with its own identity and be retained under its own key — governance
// materializes ALL of them, so a satellite has a fallback for a foreign source too.
func TestDistribution_MixedSourcesEachRetainItsOwnIdentity(t *testing.T) {
	loader := fakeLoader{
		"preset/commit.yml": "commit:\n  conventional: true\n",
		"preset/lint.yml":   "lint:\n  level: full\n",
	}
	gov := &GovernanceConfig{Profiles: ProfileList{{
		ID: "hlhd-docker",
		Config: map[string]any{
			"version": 1,
			"forges":  map[string]any{"gitlab": map[string]any{"provider": "gitlab", "url": "https://gitlab.prplanit.com", "credentials": "GITLAB"}},
			// A: unqualified — governance supplies provenance
			"commit": map[string]any{"preset": "preset/commit.yml"},
			// B: arbitrary URL
			"security": map[string]any{"preset": "https://policies.example.org/security.yml"},
			// C: another forge, tracked branch
			"release": map[string]any{"preset": "github:OtherOrg/presets//release.yml@main"},
			// D: another forge, pinned tag
			"lint": map[string]any{"preset": "github:OtherOrg/presets//lint.yml@refs/tags/v2"},
			// E: pinned sha
			"dependency": map[string]any{"preset": "github:OtherOrg/presets//dep.yml@1a2b3c4d"},
		},
		Repos: ProfileCatalog{{ID: "r", At: "HomeLabHD/apt-cacher-ng"}},
	}}}
	// Stands in for the network at reconcile time: governance materializes a foreign
	// source so the satellite receives a retained copy of it.
	prev := PresetSourceFetcher
	PresetSourceFetcher = demoFetcher{}
	defer func() { PresetSourceFetcher = prev }()

	ps := PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com", ProjectID: "PrPlanIT/MaintenancePolicy"}
	plans, err := PlanDistribution(gov, loader, nil, nil, ps, "PrPlanIT/MaintenancePolicy")
	if err != nil {
		t.Fatalf("PlanDistribution: %v", err)
	}
	var cfg string
	seeded := map[string]bool{}
	for _, f := range plans[0].Files {
		if f.Path == ".stagefreight.yml" {
			cfg = string(f.Content)
			continue
		}
		seeded[f.Path] = true
	}

	// Only the unqualified reference gains governance's provenance; every declared
	// source survives verbatim.
	for _, want := range []string{
		"preset: https://gitlab.prplanit.com/PrPlanIT/MaintenancePolicy//preset/commit.yml",
		"preset: https://policies.example.org/security.yml",
		"preset: github:OtherOrg/presets//release.yml@main",
		"preset: github:OtherOrg/presets//lint.yml@refs/tags/v2",
		"preset: github:OtherOrg/presets//dep.yml@1a2b3c4d",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("distributed config missing %q", want)
		}
	}

	// Each retained under its own identity — two sources sharing a filename cannot
	// collide, and a foreign source is seeded so the satellite has a fallback.
	for _, want := range []string{
		".stagefreight/preset-cache/https-gitlab.prplanit.com/PrPlanIT/MaintenancePolicy/preset/commit.yml",
		".stagefreight/preset-cache/https-policies.example.org/security.yml",
		".stagefreight/preset-cache/github-OtherOrg/presets-main/release.yml",
		".stagefreight/preset-cache/github-OtherOrg/presets-refs/tags/v2/lint.yml",
		".stagefreight/preset-cache/github-OtherOrg/presets-1a2b3c4d/dep.yml",
	} {
		if !seeded[want] {
			t.Errorf("not retained: %s", want)
		}
	}
}

type demoFetcher struct{}

func (demoFetcher) Fetch(source, ref, path string) ([]byte, error) {
	switch {
	case strings.Contains(source+path, "security"):
		return []byte("security:\n  sbom: true\n"), nil
	case strings.Contains(path, "release"):
		return []byte("release:\n  enabled: true\n"), nil
	case strings.Contains(path, "lint"):
		return []byte("lint:\n  level: full\n"), nil
	case strings.Contains(path, "dep"):
		return []byte("dependency:\n  enabled: true\n"), nil
	}
	return []byte("version: 1\n"), nil
}
func (demoFetcher) Classify(_, _ string) (presetref.Kind, error) { return presetref.Tracked, nil }
