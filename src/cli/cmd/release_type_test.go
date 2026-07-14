package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// TestResolveReleaseType pins the config→IR mapping: explicit latest/prerelease win;
// "unspecified" and an omitted type both fall back to inference (version suffix, the
// deprecated prerelease bool, or --prerelease ⇒ Prerelease; otherwise Auto).
func TestResolveReleaseType(t *testing.T) {
	tgt := func(typ string, legacyPre bool) *config.TargetConfig {
		return &config.TargetConfig{Type: typ, Prerelease: legacyPre}
	}
	cases := []struct {
		name       string
		t          *config.TargetConfig
		versionPre bool
		reqPre     bool
		want       forge.ReleaseType
	}{
		{"explicit latest", tgt("latest", false), false, false, forge.ReleaseTypeLatest},
		{"explicit prerelease", tgt("prerelease", false), false, false, forge.ReleaseTypePrerelease},
		{"latest overrides version prerelease", tgt("latest", false), true, false, forge.ReleaseTypeLatest},
		{"unspecified + stable", tgt("unspecified", false), false, false, forge.ReleaseTypeAuto},
		{"unspecified + version prerelease", tgt("unspecified", false), true, false, forge.ReleaseTypePrerelease},
		{"omitted + stable", tgt("", false), false, false, forge.ReleaseTypeAuto},
		{"omitted + legacy prerelease bool", tgt("", true), false, false, forge.ReleaseTypePrerelease},
		{"omitted + --prerelease", tgt("", false), false, true, forge.ReleaseTypePrerelease},
		{"nil target + stable", nil, false, false, forge.ReleaseTypeAuto},
		{"nil target + version prerelease", nil, true, false, forge.ReleaseTypePrerelease},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveReleaseType(c.t, c.versionPre, c.reqPre); got != c.want {
				t.Errorf("resolveReleaseType = %v, want %v", got, c.want)
			}
		})
	}
}
