package render

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestReleaseTagRules pins the git.tags → forge tag-gate lowering: includes
// and excludes pass through as rules, no declared sources means no gate
// (all tags spawn), and any non-RE2 pattern degrades the WHOLE gate to nil —
// a partial rule set could be more restrictive than the binary's release
// policy and suppress a real release.
func TestReleaseTagRules(t *testing.T) {
	cfg := &config.Config{}
	if got := releaseTagRules(cfg); got != nil {
		t.Errorf("no tag sources must yield nil (all tags spawn): %+v", got)
	}

	cfg.Git.Tags = config.OrderedTagSources{
		{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`},
		{ID: "not-tmp", Pattern: `!^tmp-.*`},
	}
	got := releaseTagRules(cfg)
	if len(got) != 2 || got[0].Exclude || got[0].Pattern != `^v?\d+\.\d+\.\d+$` {
		t.Fatalf("include rule: %+v", got)
	}
	if !got[1].Exclude || got[1].Pattern != `^tmp-.*` {
		t.Errorf("exclude rule must strip ! and negate: %+v", got[1])
	}

	cfg.Git.Tags = append(cfg.Git.Tags, config.TagSourceConfig{ID: "broken", Pattern: `^v[`})
	if got := releaseTagRules(cfg); got != nil {
		t.Errorf("invalid RE2 must degrade the whole gate to nil: %+v", got)
	}
}
