package governance

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestProfileCatalog_Decode covers the reshaped governance schema: profiles as an
// id-map, and the repos catalog with both entry forms — a bare-string location-only
// entry (no governed metadata) and a branded map entry (forge prefix, metadata, and a
// per-repo config override).
func TestProfileCatalog_Decode(t *testing.T) {
	yml := `
profiles:
  rebaseable-fork-docker:
    config:
      lint: { preset: "preset/lint.yml" }
    credentials: GITLAB_HOMELABHD
    repos:
      prometheus-eaton: HomeLabHD/prometheus-eaton-ups-exporter
      ark-se-server:
        at: gitlab:HomeLabHD/ark-se-server
        title: "ARK Server"
        license: MIT
        config:
          repos: { preset: "preset/other-forge.yml" }
`
	var gov GovernanceConfig
	if err := yaml.Unmarshal([]byte(yml), &gov); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gov.Profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(gov.Profiles))
	}
	p := gov.Profiles[0]
	if p.ID != "rebaseable-fork-docker" {
		t.Errorf("profile id = %q", p.ID)
	}
	if p.Credentials != "GITLAB_HOMELABHD" {
		t.Errorf("credentials = %q", p.Credentials)
	}
	if len(p.Repos) != 2 {
		t.Fatalf("got %d catalog entries, want 2", len(p.Repos))
	}

	// Bare-string entry: location only, no governed metadata, no per-repo config.
	bare := p.Repos[0]
	if bare.ID != "prometheus-eaton" || bare.At != "HomeLabHD/prometheus-eaton-ups-exporter" {
		t.Errorf("bare entry = %+v", bare)
	}
	if bare.Forge != "" || bare.Metadata != nil || bare.Config != nil {
		t.Errorf("bare entry should carry no forge/metadata/config: %+v", bare)
	}

	// Branded map entry: forge prefix split out, metadata captured, per-repo config kept.
	branded := p.Repos[1]
	if branded.ID != "ark-se-server" || branded.Forge != "gitlab" || branded.At != "HomeLabHD/ark-se-server" {
		t.Errorf("branded entry = %+v", branded)
	}
	if branded.Metadata["title"] != "ARK Server" || branded.Metadata["license"] != "MIT" {
		t.Errorf("branded metadata = %v", branded.Metadata)
	}
	if _, ok := branded.Metadata["at"]; ok {
		t.Error("`at` must not leak into metadata")
	}
	if _, ok := branded.Metadata["config"]; ok {
		t.Error("`config` must not leak into metadata")
	}
	if branded.Config == nil {
		t.Error("per-repo config override missing")
	}
}
