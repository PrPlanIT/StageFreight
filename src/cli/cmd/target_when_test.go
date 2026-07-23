package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestTargetWhenMatches_Events pins event-aware target matching: a push-only
// target matches on a branch push (no tag in env) and is rejected on a tag
// pipeline; a tag-only target is the inverse; a target with no events: is
// unaffected — the stable-release regression guard (events must enforce only when
// declared, and the push-vs-tag signal comes from env tag presence, not a tag
// string, so Commit 7's synthesized dev tag won't flip a push to a tag).
func TestTargetWhenMatches_Events(t *testing.T) {
	pushTarget := config.TargetConfig{When: config.WhenConditions{{Events: []string{"push"}}}}
	tagTarget := config.TargetConfig{When: config.WhenConditions{{Events: []string{"tag"}}}}
	noEvents := config.TargetConfig{When: config.WhenConditions{{}}}

	t.Run("push build (no tag env)", func(t *testing.T) {
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("CI_COMMIT_TAG", "")
		t.Setenv("SF_CI_EVENT", "push")
		if !targetWhenMatches(pushTarget, "", nil, nil) {
			t.Error("push target should match on a push")
		}
		if targetWhenMatches(tagTarget, "", nil, nil) {
			t.Error("tag target should NOT match on a push")
		}
		if !targetWhenMatches(noEvents, "", nil, nil) {
			t.Error("no-events target should always match (regression)")
		}
	})

	t.Run("tag build (CI_COMMIT_TAG set)", func(t *testing.T) {
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("CI_COMMIT_TAG", "v1.2.3")
		t.Setenv("SF_CI_EVENT", "push") // GitLab reports push even for tags — tag presence wins
		if !targetWhenMatches(tagTarget, "v1.2.3", nil, nil) {
			t.Error("tag target should match on a tag")
		}
		if targetWhenMatches(pushTarget, "v1.2.3", nil, nil) {
			t.Error("push target should NOT match on a tag")
		}
		if !targetWhenMatches(noEvents, "v1.2.3", nil, nil) {
			t.Error("no-events target should still match on a tag (stable-release regression)")
		}
	})
}
