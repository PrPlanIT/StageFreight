package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// The publish phase must reach the project-identity sync (registry overviews + forge repo
// descriptions). metadataTargetsDue is the selection publishPhaseRunner feeds to
// RunMetadataSection; if it silently returns nothing (or the wiring is dropped, as the
// e1f2189 migration did), the docker README/description never syncs. This locks in that a
// kind:metadata target whose when: matches the event IS selected, and that wrong-kind or
// non-matching targets are not.
func TestMetadataTargetsDue(t *testing.T) {
	t.Setenv("CI_COMMIT_BRANCH", "main")
	t.Setenv("SF_CI_TAG", "")
	t.Setenv("CI_COMMIT_TAG", "")

	cfg := &config.Config{}
	cfg.Git.Branches = map[string]string{"main": "^main$", "dev": "^dev$"}
	cfg.Targets = []config.TargetConfig{
		{ID: "meta-main", Kind: "metadata", When: config.WhenConditions{
			{Branches: []string{"main"}, Events: []string{"push", "tag"}}}}, // matches push/main
		{ID: "meta-dev", Kind: "metadata", When: config.WhenConditions{
			{Branches: []string{"dev"}, Events: []string{"push"}}}}, // wrong branch → excluded
		{ID: "img", Kind: "registry", Registry: config.StringOrList{"dockerhub"}}, // wrong kind → excluded
	}

	due := metadataTargetsDue(cfg)
	if len(due) != 1 || due[0].ID != "meta-main" {
		got := make([]string, len(due))
		for i, d := range due {
			got[i] = d.ID
		}
		t.Fatalf("push/main → %v, want [meta-main] (only the metadata target whose when: matches)", got)
	}

	// On a branch the metadata target does not gate for, nothing is due.
	t.Setenv("CI_COMMIT_BRANCH", "feature/x")
	if due := metadataTargetsDue(cfg); len(due) != 0 {
		t.Errorf("push/feature-x → %d targets, want 0 (no metadata when: matches)", len(due))
	}
}
