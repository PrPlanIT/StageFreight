package governance

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// countingFetcher records whether validation reached the network.
type countingFetcher struct{ n *int }

func (c countingFetcher) Fetch(_, _, path string) ([]byte, error) {
	*c.n++
	if strings.Contains(path, "lint") {
		return []byte("lint:\n  level: full\n"), nil
	}
	return []byte("version: 1\n"), nil
}
func (c countingFetcher) Classify(_, _ string) (presetref.Kind, error) { return presetref.Tracked, nil }

// Validating a render must resolve from the cache being distributed WITH it, not from
// the network: the question is whether what we hand over is self-sufficient. Fetching
// there would also repeat every reference once per governed repo.
func TestValidationDoesNotFetchPerSatellite(t *testing.T) {
	fetches := 0
	prev := PresetSourceFetcher
	PresetSourceFetcher = countingFetcher{n: &fetches}
	defer func() { PresetSourceFetcher = prev }()

	loader := fakeLoader{"preset/lint.yml": "lint:\n  level: full\n"}
	gov := &GovernanceConfig{Profiles: ProfileList{{
		ID: "p",
		Config: map[string]any{
			"version": 1,
			"forges":  map[string]any{"gitlab": map[string]any{"provider": "gitlab", "url": "https://gl.example.com", "credentials": "GITLAB"}},
			"lint":    map[string]any{"preset": "preset/lint.yml"},
		},
		// Three members: a per-satellite fetch would scale with this.
		Repos: ProfileCatalog{{ID: "a", At: "Org/a"}, {ID: "b", At: "Org/b"}, {ID: "c", At: "Org/c"}},
	}}}
	ps := PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gl.example.com", ProjectID: "Org/policy"}

	if _, err := PlanDistribution(gov, loader, nil, nil, ps, "Org/policy"); err != nil {
		t.Fatalf("PlanDistribution: %v", err)
	}
	// The one preset is governance-owned, so materialization reads it locally and
	// validation reads the seeded cache — neither needs the network.
	if fetches != 0 {
		t.Errorf("validation fetched %d times; a render is verified against the cache it ships with", fetches)
	}
}
