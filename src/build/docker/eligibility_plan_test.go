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
			Git: config.GitConfig{
				Branches: map[string]string{"main": `^main$`},
				Tags:     config.OrderedTagSources{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}},
			},
			Registries: []config.RegistryConfig{{ID: "dh", Provider: "docker", URL: "docker.io", DefaultPath: "acme/img"}},
			Builds:     []config.BuildConfig{{ID: "img", Kind: "docker", Dockerfile: "Dockerfile"}},
			Targets: []config.TargetConfig{
				{ID: "dev", Kind: "registry", Build: "img", Registry: config.StringOrList{"dh"},
					Tags: []string{"latest-dev"}, When: config.WhenConditions{{Branches: []string{"main"}, Events: []string{"push"}}}},
				{ID: "stable", Kind: "registry", Build: "img", Registry: config.StringOrList{"dh"},
					Tags: []string{"latest"}, When: config.WhenConditions{{GitTags: []string{"stable"}, Events: []string{"tag"}}}},
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

func TestImagePlanRows(t *testing.T) {
	dev := []build.RegistryTarget{
		{URL: "docker.io", Tags: []string{"dev-128cd8f", "latest-dev"}},
		{URL: "cr.optcp.com", Tags: []string{"dev-128cd8f", "latest-dev"}},
	}
	devTags := []string{"a", "b", "c", "d"}
	skips := func(gate string, n int) []build.TargetSkip {
		out := make([]build.TargetSkip, n)
		for i := range out {
			out[i] = build.TargetSkip{TargetID: "stable-" + string(rune('a'+i)), Reason: "matcher reason", Gate: gate}
		}
		return out
	}
	amd64 := []string{"linux/amd64"}

	cases := []struct {
		name           string
		steps          []build.BuildStep
		local, verbose bool
		want           []string
	}{
		{
			name:  "publishes and holds",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64, Tags: devTags, Registries: dev, SkippedTargets: skips("events: tag", 6)}},
			want: []string{
				"image     1 build(s) · linux/amd64 · 4 tag(s)",
				"image     publish   docker.io      dev-128cd8f, latest-dev",
				"image     publish   cr.optcp.com   dev-128cd8f, latest-dev",
				"image     ⊘ hold    6 targets      events: tag",
			},
		},
		{
			name:  "everything held",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64, SkippedTargets: skips("events: tag", 6)}},
			want: []string{
				"image     1 build(s) · linux/amd64 · 0 tag(s)",
				"image     ⊘ hold   6 targets   events: tag",
			},
		},
		{
			name: "hold reasons group separately",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64,
				SkippedTargets: append(skips("git_tags: prerelease", 3), skips("branches: main", 1)...)}},
			want: []string{
				"image     1 build(s) · linux/amd64 · 0 tag(s)",
				"image     ⊘ hold   3 targets   git_tags: prerelease",
				"image     ⊘ hold   1 target    branches: main",
			},
		},
		{
			name: "two builds name each row",
			steps: []build.BuildStep{
				{Name: "zitadel", Platforms: amd64, Tags: devTags[:2], Registries: dev[:1]},
				{Name: "login", Platforms: amd64, Tags: devTags[:2], Registries: dev[1:]},
			},
			want: []string{
				"image     2 build(s) · linux/amd64 · 4 tag(s)",
				"image     zitadel   publish   docker.io      dev-128cd8f, latest-dev",
				"image     login     publish   cr.optcp.com   dev-128cd8f, latest-dev",
			},
		},
		{
			name:  "tags differ by registry",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64, Tags: devTags[:3], Registries: []build.RegistryTarget{{URL: "docker.io", Tags: []string{"v1", "latest"}}, {URL: "cr.optcp.com", Tags: []string{"v1"}}}}},
			want: []string{
				"image     1 build(s) · linux/amd64 · 3 tag(s)",
				"image     publish   docker.io      v1, latest",
				"image     publish   cr.optcp.com   v1",
			},
		},
		{
			name:  "local loads and publishes nothing",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64}},
			local: true,
			want: []string{
				"image     1 build(s) · linux/amd64 · 1 tag(s)",
				"image     load   docker   stagefreight:dev",
			},
		},
		{
			name:  "no targets",
			steps: []build.BuildStep{{Name: "img", Platforms: amd64}},
			want:  []string{"image     1 build(s) · linux/amd64 · 0 tag(s)"},
		},
		{
			name:    "verbose lists each held target with the matcher's reason",
			steps:   []build.BuildStep{{Name: "img", Platforms: amd64, SkippedTargets: skips("events: tag", 2)}},
			verbose: true,
			want: []string{
				"image     1 build(s) · linux/amd64 · 0 tag(s)",
				"image     ⊘ hold   stable-a   matcher reason",
				"image     ⊘ hold   stable-b   matcher reason",
			},
		},
		{
			name:  "platforms render in a stable order",
			steps: []build.BuildStep{{Name: "img", Platforms: []string{"linux/arm64", "linux/amd64"}}},
			want:  []string{"image     1 build(s) · linux/amd64, linux/arm64 · 0 tag(s)"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := imagePlanRows(c.steps, c.local, c.verbose, false)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}
