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
