package governance

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

type presetStubFetcher struct{}

func (presetStubFetcher) Fetch(_, _, _ string) ([]byte, error) {
	return []byte("lint:\n  level: full\n"), nil
}
func (presetStubFetcher) Classify(_, _, _ string) (presetref.Kind, error) {
	return presetref.Tracked, nil
}

// A satellite needs the foreign source's content to resolve offline, and nothing beside
// it. What the source points at tracks the source, not the content: seeding it writes a
// new value into every satellite whenever the policy repo moves, so runs that changed no
// preset still commit everywhere.
func TestDistributionSeedsContentOnly(t *testing.T) {
	prev := PresetSourceFetcher
	PresetSourceFetcher = presetStubFetcher{}
	defer func() { PresetSourceFetcher = prev }()

	gov := &GovernanceConfig{Profiles: ProfileList{{
		ID: "p",
		Config: map[string]any{
			"version": 1,
			"forges":  map[string]any{"gitlab": map[string]any{"provider": "gitlab", "url": "https://gl.example.com", "credentials": "GITLAB"}},
			"lint":    map[string]any{"preset": "https://policies.example.org/lint.yml"},
		},
		Repos: ProfileCatalog{{ID: "r", At: "Org/repo"}},
	}}}
	ps := PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gl.example.com", ProjectID: "Org/policy"}

	plans, err := PlanDistribution(gov, fakeLoader{}, nil, nil, ps, "Org/policy")
	if err != nil {
		t.Fatalf("PlanDistribution: %v", err)
	}

	var content bool
	for _, f := range plans[0].Files {
		if strings.HasSuffix(f.Path, ".revision") {
			t.Errorf("seeded %q beside the content: it moves with the source, so every run rewrites every satellite", f.Path)
		}
		if strings.Contains(f.Path, "policies.example.org") {
			content = true
		}
	}
	if !content {
		t.Error("foreign source content was not seeded")
	}
}
