package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// planRegistryRetention must select only REMOTE registry targets that have active
// retention — local registries are perform's job, and targets without retention
// are skipped. Produced tags are folded into protect.
func TestPlanRegistryRetention_RemoteWithRetentionOnly(t *testing.T) {
	t.Setenv("SF_CI_EVENT", "push")

	cfg := &config.Config{
		Registries: []config.RegistryConfig{
			{ID: "dockerhub", Provider: "docker", URL: "docker.io", Credentials: "DOCKER", DefaultPath: "org/app"},
			{ID: "localreg", Provider: "local", URL: "local", DefaultPath: "org/app"},
		},
		Targets: []config.TargetConfig{
			{ID: "dh-dev", Kind: "registry", Registry: "dockerhub",
				Tags: []string{"dev-{sha:8}", "latest-dev"}, Retention: &config.RetentionPolicy{KeepLast: 6, Protect: []string{"latest-dev"}}},
			{ID: "local-dev", Kind: "registry", Registry: "localreg",
				Tags: []string{"dev-{sha:8}"}, Retention: &config.RetentionPolicy{KeepLast: 3}},
			{ID: "dh-norit", Kind: "registry", Registry: "dockerhub", Tags: []string{"x"}}, // no retention
		},
	}
	vi := &gitver.VersionInfo{Version: "1.0.0-dev+abc12345", Base: "1.0.0", SHA: "abc12345"}

	jobs := planRegistryRetention(cfg, vi)
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1 (remote+retention only): %+v", len(jobs), jobs)
	}
	j := jobs[0]
	if j.provider != "docker" {
		t.Errorf("provider = %q, want docker (local must be excluded)", j.provider)
	}
	if len(j.tagPatterns) != 2 || j.tagPatterns[0] != "dev-{sha:8}" {
		t.Errorf("tagPatterns = %v, want the target's tag templates", j.tagPatterns)
	}

	// protect = configured protect + the concrete tags produced this run.
	foundLatestDev, foundResolved := false, false
	for _, p := range j.policy.Protect {
		switch p {
		case "latest-dev":
			foundLatestDev = true
		case "dev-abc12345":
			foundResolved = true
		}
	}
	if !foundLatestDev || !foundResolved {
		t.Errorf("protect = %v, want to include latest-dev and the produced dev-abc12345", j.policy.Protect)
	}
}
