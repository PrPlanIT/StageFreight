package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGovernancePayloadIsOpaqueAtLoad verifies a governance profile's config: block is
// NOT preset-resolved at the control repo's own load — its presets (which reference the
// per-satellite layout and use per-repo {var:}) resolve at DISTRIBUTION. So a config:
// preset ref that doesn't exist locally must not be loaded (and error) at control-repo
// load; opacity leaves the ref untouched for the distributor.
func TestGovernancePayloadIsOpaqueAtLoad(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
lifecycle: { mode: governance }
forges:
  gitlab: { provider: gitlab, url: "https://x", credentials: GITLAB }
repos:
  primary: { forge: gitlab, project: "Org/Repo", roles: [primary], branches: { default: main } }
governance:
  profiles:
    p1:
      credentials: GITLAB_X
      config:
        registries: { preset: "preset/does-not-exist.yml" }
      repos:
        r1: Org/some-repo
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("governance payload must be opaque at load (dangling preset + {var:} inside "+
			"governance.profiles.config should not break control-repo load): %v", err)
	}
}
