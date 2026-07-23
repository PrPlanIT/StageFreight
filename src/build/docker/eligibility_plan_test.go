package docker

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestPlanDockerBuildEventGating is the guardrail-5 proof: the docker plan routes
// target eligibility through the canonical config.TargetEligibility, so the
// tag-gated stable distribution path does NOT regress. A tag pipeline resolves the
// stable (events:[tag]) target and narrates the dev skip; a push resolves the dev
// (events:[push]) target and narrates the stable skip — the matcher's reason
// carried straight onto the step.
func TestPlanDockerBuildEventGating(t *testing.T) {
	mkConfig := func() *config.Config {
		return &config.Config{
			Versioning: config.VersioningConfig{
				TagSources: []config.TagSourceConfig{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}},
			},
			Matchers:   config.MatchersConfig{Branches: map[string]string{"main": `^main$`}},
			Registries: []config.RegistryConfig{{ID: "dh", Provider: "docker", URL: "docker.io", DefaultPath: "acme/img"}},
			Builds:     []config.BuildConfig{{ID: "img", Kind: "docker", Dockerfile: "Dockerfile"}},
			Targets: []config.TargetConfig{
				{ID: "dev", Kind: "registry", Build: "img", Registry: config.StringOrList{"dh"},
					Tags: []string{"latest-dev"}, When: config.TargetCondition{Branches: []string{"main"}, Events: []string{"push"}}},
				{ID: "stable", Kind: "registry", Build: "img", Registry: config.StringOrList{"dh"},
					Tags: []string{"latest"}, When: config.TargetCondition{GitTags: []string{"stable"}, Events: []string{"tag"}}},
			},
		}
	}

	planFor := func(t *testing.T, gitTag string) *build.BuildStep {
		t.Helper()
		cfg := mkConfig()
		det := &build.Detection{RootDir: "."}
		vi := &build.VersionInfo{Version: "0.6.1", Base: "0.6.1", SHA: "abc1234", Branch: "main"}
		step, err := planDockerBuild(cfg.Builds[0], cfg, det, vi, "main", gitTag)
		if err != nil {
			t.Fatalf("planDockerBuild(gitTag=%q): %v", gitTag, err)
		}
		return step
	}

	hasTag := func(step *build.BuildStep, tag string) bool {
		for _, r := range step.Registries {
			for _, tg := range r.Tags {
				if tg == tag {
					return true
				}
			}
		}
		return false
	}
	skipReason := func(step *build.BuildStep, id string) (string, bool) {
		for _, s := range step.SkippedTargets {
			if s.TargetID == id {
				return s.Reason, true
			}
		}
		return "", false
	}

	t.Run("tag resolves stable, narrates dev skip", func(t *testing.T) {
		t.Setenv("CI_COMMIT_TAG", "v0.6.1") // → config.CIEvent()=="tag"
		step := planFor(t, "v0.6.1")
		if !hasTag(step, "latest") {
			t.Errorf("stable target must resolve on a tag; registries=%+v", step.Registries)
		}
		if reason, ok := skipReason(step, "dev"); !ok {
			t.Errorf("dev target must be skipped on a tag")
		} else if !strings.Contains(reason, "events") {
			t.Errorf("dev skip reason must cite the event gate, got %q", reason)
		}
	})

	t.Run("push resolves dev, narrates stable skip", func(t *testing.T) {
		t.Setenv("SF_CI_EVENT", "push")
		t.Setenv("CI_COMMIT_TAG", "")
		step := planFor(t, "")
		if !hasTag(step, "latest-dev") {
			t.Errorf("dev target must resolve on a push; registries=%+v", step.Registries)
		}
		if reason, ok := skipReason(step, "stable"); !ok {
			t.Errorf("stable target must be skipped on a push")
		} else if !strings.Contains(reason, "events") {
			t.Errorf("stable skip reason must cite the event gate, got %q", reason)
		}
	})
}
