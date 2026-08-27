package governance

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPlanDistribution_BrandedEntryDistributesMetadata proves the Phase B/C payoff:
// a BRANDED catalog entry (title/description/topics/license, anchored on a location)
// lands as the satellite's metadata: block in its sealed .stagefreight.yml, with org
// DERIVED from the location's group — the satellite never re-declares its identity.
// This mirrors MaintenancePolicy's prometheus-eaton-ups-exporter entry.
func TestPlanDistribution_BrandedEntryDistributesMetadata(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "rebaseable-fork-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
			},
			Repos: ProfileCatalog{{
				ID: "prometheus-eaton-ups-exporter",
				At: "HomeLabHD/prometheus-eaton-ups-exporter",
				Metadata: map[string]any{
					"title": "Eaton UPS Exporter",
					"description": []any{
						"Prometheus exporter for Eaton UPS devices over SNMP/HTTP.",
						"Rootless container exporting Eaton UPS load, runtime, battery, and I/O metrics to Prometheus.",
					},
					"topics":  []any{"prometheus", "exporter", "ups", "eaton", "monitoring", "snmp"},
					"license": "MIT",
				},
			}},
		}},
	}

	plans, err := PlanDistribution(
		gov,
		fakeLoader{},
		nil, // no assets
		nil, // no forge reader → every file is a create
		PresetSourceInfo{
			Provider:  "gitlab",
			ForgeURL:  "https://gitlab.prplanit.com",
			ProjectID: "PrPlanIT/MaintenancePolicy",
			Ref:       "deadbeef",
		},
		"PrPlanIT/MaintenancePolicy",
	)
	requireNoError(t, err)

	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	plan := plans[0]
	if plan.Repo != "HomeLabHD/prometheus-eaton-ups-exporter" {
		t.Fatalf("plan targets wrong repo: %q", plan.Repo)
	}
	if plan.Credentials != "GITLAB_HOMELABHD" {
		t.Fatalf("plan carries wrong credentials: %q", plan.Credentials)
	}

	// Find the sealed .stagefreight.yml and parse its metadata: block.
	var sealed []byte
	for _, f := range plan.Files {
		if f.Path == ".stagefreight.yml" {
			sealed = f.Content
		}
	}
	if sealed == nil {
		t.Fatal("no .stagefreight.yml in the plan")
	}

	var parsed struct {
		Metadata map[string]any `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(sealed, &parsed); err != nil {
		t.Fatalf("sealed config is not valid YAML: %v", err)
	}
	if parsed.Metadata == nil {
		t.Fatalf("branded entry did not distribute a metadata: block; got:\n%s", sealed)
	}

	// org is DERIVED from the location group — never re-declared by the satellite.
	if got := parsed.Metadata["org"]; got != "HomeLabHD" {
		t.Errorf("metadata.org: want HomeLabHD (derived from location), got %v", got)
	}
	if got := parsed.Metadata["title"]; got != "Eaton UPS Exporter" {
		t.Errorf("metadata.title: want %q, got %v", "Eaton UPS Exporter", got)
	}
	if got := parsed.Metadata["license"]; got != "MIT" {
		t.Errorf("metadata.license: want MIT, got %v", got)
	}
	topics, _ := parsed.Metadata["topics"].([]any)
	if len(topics) != 6 {
		t.Errorf("metadata.topics: want 6 topics, got %v", parsed.Metadata["topics"])
	}
	if !strings.Contains(string(sealed), "Rootless container exporting Eaton UPS") {
		t.Errorf("sealed config dropped the tiered description tail:\n%s", sealed)
	}
}

// TestPlanDistribution_ConcretizesRepos proves the vars→facts collapse's distribution
// half: the catalog entry's AUTHORITATIVE location becomes the satellite's CONCRETE
// repos section (no shared repos preset with var-holes, no slug circularity) — primary
// on the source provider's forge at the entry location, plus a github mirror derived
// from the org's github alias. metadata.org is always injected (a coordinate, not
// branding), so the satellite resolves {org.*}/{path.*}/{slug} at its own load.
func TestPlanDistribution_ConcretizesRepos(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "rebaseable-fork-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"orgs": map[string]any{
					"HomeLabHD": map[string]any{
						"maintainer": "HomeLabHD <homelabhelp@gmail.com>",
						"aliases":    map[string]any{"handle": "hlhd", "github": "PrPlanIT", "docker": "prplanit"},
					},
				},
			},
			Repos: ProfileCatalog{{
				ID: "prometheus-eaton-ups-exporter",
				At: "HomeLabHD/prometheus-eaton-ups-exporter",
			}},
		}},
	}

	plans, err := PlanDistribution(gov, fakeLoader{}, nil, nil,
		PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com", ProjectID: "PrPlanIT/MaintenancePolicy", Ref: "deadbeef"},
		"PrPlanIT/MaintenancePolicy")
	requireNoError(t, err)

	var sealed []byte
	for _, f := range plans[0].Files {
		if f.Path == ".stagefreight.yml" {
			sealed = f.Content
		}
	}
	var parsed struct {
		Metadata map[string]any            `yaml:"metadata"`
		Repos    map[string]map[string]any `yaml:"repos"`
	}
	if err := yaml.Unmarshal(sealed, &parsed); err != nil {
		t.Fatalf("sealed config invalid YAML: %v", err)
	}

	// Location-only entry STILL gets metadata.org — the coordinate every fact needs.
	if got := parsed.Metadata["org"]; got != "HomeLabHD" {
		t.Errorf("metadata.org: want HomeLabHD (derived), got %v", got)
	}

	primary := parsed.Repos["primary"]
	if primary == nil {
		t.Fatalf("sealed config must carry a concrete primary repo, got repos=%v", parsed.Repos)
	}
	if primary["project"] != "HomeLabHD/prometheus-eaton-ups-exporter" || primary["forge"] != "gitlab" {
		t.Errorf("primary must be the entry location on the source forge, got %v", primary)
	}
	if _, hasTemplate := primary["project"].(string); hasTemplate && strings.Contains(primary["project"].(string), "{") {
		t.Errorf("primary project must be CONCRETE, got %v", primary["project"])
	}

	mirror := parsed.Repos["github-mirror"]
	if mirror == nil {
		t.Fatalf("org with a github alias must yield a github mirror, got repos=%v", parsed.Repos)
	}
	if mirror["project"] != "PrPlanIT/prometheus-eaton-ups-exporter" || mirror["forge"] != "github" {
		t.Errorf("mirror must derive from the github alias + slug, got %v", mirror)
	}
}

// TestPlanDistribution_CachesComposedPresets proves a `presets: [a, b]` composition has
// BOTH files seeded into the satellite's preset-cache — else a tracking satellite fails to
// resolve the composed section ("open .../preset-cache/<path>: no such file").
func TestPlanDistribution_CachesComposedPresets(t *testing.T) {
	loader := fakeLoader{
		"preset/stencils-core.yml":   "stencils: { license: { label: license } }",
		"preset/stencils-docker.yml": "stencils: { docker: { render: shield } }",
	}
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID: "p1",
			Config: map[string]any{
				"stencils": map[string]any{
					"presets": []any{"preset/stencils-core.yml", "preset/stencils-docker.yml"},
				},
			},
			Repos: ProfileCatalog{{ID: "r1", At: "Org/some-repo"}},
		}},
	}

	plans, err := PlanDistribution(gov, loader, nil, nil, PresetSourceInfo{Ref: "deadbeef"}, "Org/policy")
	requireNoError(t, err)

	want := map[string]bool{
		".stagefreight/preset-cache/preset/stencils-core.yml":   false,
		".stagefreight/preset-cache/preset/stencils-docker.yml": false,
	}
	for _, f := range plans[0].Files {
		if _, ok := want[f.Path]; ok {
			want[f.Path] = true
		}
	}
	for path, cached := range want {
		if !cached {
			t.Errorf("composed preset %q was not seeded into the preset-cache", path)
		}
	}
}
