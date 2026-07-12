package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestResolveMirrorPrerelease(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.TargetConfig{
			// stable channel — not a prerelease
			{ID: "primary-release", Kind: "release", Aliases: []string{"v{version}", "latest"}},
			// rolling dev channel — prerelease
			{ID: "dev-release", Kind: "release", Tag: "dev-{sha:8}", Aliases: []string{"latest-dev"}, Prerelease: true},
		},
	}

	cases := []struct {
		name    string
		tag     string
		body    string
		want    bool
	}{
		{"dev sha tag matches prerelease channel", "dev-046e872", "", true},
		{"rolling alias matches prerelease channel", "latest-dev", "", true},
		{"stable version tag is not prerelease", "v0.7.0", "", false},
		{"stable stays false even with stable marker", "v0.7.0", "**Release type:** stable", false},
		{"body marker alone flags prerelease when no channel matches", "orphan-xyz", "notes\n**Release type:** prerelease\n", true},
		{"unknown tag with no marker is not prerelease", "orphan-xyz", "just notes", false},
		{"anchored: latest does not match latest-dev's owner", "latest", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMirrorPrerelease(cfg, tc.tag, tc.body); got != tc.want {
				t.Fatalf("resolveMirrorPrerelease(%q, body=%q) = %v, want %v", tc.tag, tc.body, got, tc.want)
			}
		})
	}

	// nil config must not panic and defaults to false (no body signal).
	if resolveMirrorPrerelease(nil, "dev-1", "") {
		t.Fatal("nil cfg with no body marker should be false")
	}
}
