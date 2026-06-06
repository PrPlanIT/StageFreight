package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func devChannelCfg() *config.Config {
	return &config.Config{
		Matchers: config.MatchersConfig{Branches: map[string]string{"main": `^main$`}},
		Targets: []config.TargetConfig{
			{ID: "dwiz-dev", Kind: "release", Tag: "dev-{sha:8}", Aliases: []string{"latest-dev"},
				When: config.TargetCondition{Branches: []string{"main"}, Events: []string{"push"}}},
		},
	}
}

// TestChannelTagTarget pins push-channel detection: a release target with a Tag
// pattern matching the current env is selected; a tag pipeline (wrong event)
// selects none; a config without channel targets selects none.
func TestChannelTagTarget(t *testing.T) {
	cfg := devChannelCfg()

	t.Run("push to main selects the channel", func(t *testing.T) {
		t.Setenv("CI_COMMIT_TAG", "")
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("SF_CI_EVENT", "push")
		t.Setenv("CI_COMMIT_BRANCH", "main")
		got := channelTagTarget(cfg)
		if got == nil || got.ID != "dwiz-dev" {
			t.Fatalf("expected dwiz-dev channel, got %v", got)
		}
	})

	t.Run("tag pipeline selects nothing (events: push)", func(t *testing.T) {
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("CI_COMMIT_TAG", "v1.2.3")
		t.Setenv("CI_COMMIT_BRANCH", "")
		if got := channelTagTarget(cfg); got != nil {
			t.Fatalf("expected nil on tag pipeline, got %v", got)
		}
	})

	t.Run("no channel targets", func(t *testing.T) {
		t.Setenv("CI_COMMIT_TAG", "")
		t.Setenv("CI_COMMIT_BRANCH", "main")
		t.Setenv("SF_CI_EVENT", "push")
		plain := &config.Config{Targets: []config.TargetConfig{{ID: "img", Kind: "registry"}}}
		if got := channelTagTarget(plain); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

// TestReleaseTagMatchesAnyTarget_EventsOnly pins that a release target gated only
// by events: counts as a constraint match, so a push-only channel passes the
// policy gate even without git_tags/branches.
func TestReleaseTagMatchesAnyTarget_EventsOnly(t *testing.T) {
	t.Setenv("SF_CI_TAG", "")
	t.Setenv("CI_COMMIT_TAG", "")
	t.Setenv("SF_CI_EVENT", "push")
	t.Setenv("CI_COMMIT_BRANCH", "main")
	cfg := &config.Config{
		Targets: []config.TargetConfig{
			{ID: "dev", Kind: "release", Tag: "dev-{sha:8}",
				When: config.TargetCondition{Events: []string{"push"}}},
		},
	}
	if !releaseTagMatchesAnyTarget(cfg, "dev-abc12345") {
		t.Error("events-only release target should satisfy the policy gate on a push")
	}
}
