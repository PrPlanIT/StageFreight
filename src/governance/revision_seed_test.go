package governance

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

type revStubFetcher struct{ rev string }

func (r revStubFetcher) Fetch(_, _, _ string) ([]byte, error) {
	return []byte("lint:\n  level: full\n"), nil
}
func (r revStubFetcher) Classify(_, _ string) (presetref.Kind, error) { return presetref.Tracked, nil }
func (r revStubFetcher) Revision(_, _ string) (string, error)         { return r.rev, nil }

// CI clones fresh every job, so a revision recorded only at runtime is gone by the next
// one — the satellite would re-transfer every source just to learn nothing changed.
// Seeding it beside the content is what makes the cheap re-check work off a laptop.
func TestDistributionSeedsRevisionBesideContent(t *testing.T) {
	prev := PresetSourceFetcher
	PresetSourceFetcher = revStubFetcher{rev: "abc123"}
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

	var content, revision bool
	for _, f := range plans[0].Files {
		if strings.HasSuffix(f.Path, presetref.RevisionSuffix) {
			revision = true
			if string(f.Content) != "abc123" {
				t.Errorf("seeded revision = %q, want the source's", f.Content)
			}
			continue
		}
		if strings.Contains(f.Path, "policies.example.org") {
			content = true
		}
	}
	if !content {
		t.Error("foreign source content was not seeded")
	}
	if !revision {
		t.Error("no revision seeded — the satellite would re-transfer to learn nothing changed")
	}
}
