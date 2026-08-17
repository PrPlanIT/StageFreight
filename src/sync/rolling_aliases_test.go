package sync

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// B — rolling aliases are classified FROM CONFIG: kind:release alias templates without a
// version/sha placeholder are rolling (force-eligible); templates that resolve to a
// version identity are excluded, so a version tag is never in the force set.
func TestRollingAliasTagSet_ClassifiesFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Targets = config.OrderedTargets{
		{Kind: "release", Aliases: []string{"latest", "latest-dev", "{version}", "v{version}"}},
		{Kind: "registry", Aliases: []string{"ignored-non-release"}},
	}
	vi := &gitver.VersionInfo{Version: "1.2.3", Major: "1", Minor: "2", Patch: "3", SHA: "abcdef1"}

	set := RollingAliasTagSet(cfg, vi)

	if !set["latest"] || !set["latest-dev"] {
		t.Errorf("rolling aliases must be present; got %v", set)
	}
	if set["1.2.3"] || set["v1.2.3"] {
		t.Errorf("immutable version identities must be excluded; got %v", set)
	}
	if set["ignored-non-release"] {
		t.Errorf("non-release target aliases must be ignored; got %v", set)
	}
}

// With no version info, only literal (placeholder-free) rolling names are admitted — the
// version identities need resolution and stay out, so nothing immutable leaks in.
func TestRollingAliasTagSet_NilVersionAdmitsLiterals(t *testing.T) {
	cfg := &config.Config{}
	cfg.Targets = config.OrderedTargets{
		{Kind: "release", Aliases: []string{"latest", "{version}"}},
	}
	set := RollingAliasTagSet(cfg, nil)
	if !set["latest"] {
		t.Errorf("literal rolling alias latest must be present; got %v", set)
	}
	if len(set) != 1 {
		t.Errorf("only the literal should be admitted; got %v", set)
	}
}

func TestIsImmutableTagTemplate(t *testing.T) {
	for _, im := range []string{"{version}", "v{version}", "dev-{sha:8}", "{major}.{minor}", "{patch}"} {
		if !IsImmutableTagTemplate(im) {
			t.Errorf("%q should be immutable", im)
		}
	}
	for _, roll := range []string{"latest", "latest-dev", "nightly", "stable"} {
		if IsImmutableTagTemplate(roll) {
			t.Errorf("%q should be rolling", roll)
		}
	}
}
