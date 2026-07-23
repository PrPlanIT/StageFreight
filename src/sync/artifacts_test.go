package sync

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

func rel(tag string, draft bool) forge.ReleaseInfo {
	return forge.ReleaseInfo{TagName: tag, Draft: draft}
}

func relTags(rs []forge.ReleaseInfo) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.TagName)
	}
	return strings.Join(out, ",")
}

func TestScopedReleases_AllExcludesDrafts(t *testing.T) {
	primary := []forge.ReleaseInfo{rel("v1", false), rel("v2", true), rel("v3", false)}
	if got := relTags(ScopedReleases(primary, &config.FacetSpec{Scope: "all"}, "")); got != "v1,v3" {
		t.Fatalf("scope:all should drop drafts, got %q", got)
	}
}

func TestScopedReleases_DraftsIncluded(t *testing.T) {
	primary := []forge.ReleaseInfo{rel("v1", false), rel("v2", true)}
	if got := relTags(ScopedReleases(primary, &config.FacetSpec{Scope: "all", Drafts: true}, "")); got != "v1,v2" {
		t.Fatalf("drafts:true should carry drafts, got %q", got)
	}
}

func TestScopedReleases_Current(t *testing.T) {
	primary := []forge.ReleaseInfo{rel("v1", false), rel("v2", false)}
	if got := relTags(ScopedReleases(primary, &config.FacetSpec{Scope: "current"}, "v2")); got != "v2" {
		t.Fatalf("scope:current should keep only the run's tag, got %q", got)
	}
}

func TestScopedReleases_Match(t *testing.T) {
	primary := []forge.ReleaseInfo{rel("v1.0", false), rel("beta-1", false)}
	if got := relTags(ScopedReleases(primary, &config.FacetSpec{Scope: "all", Match: "v*"}, "")); got != "v1.0" {
		t.Fatalf("match should filter by tag glob, got %q", got)
	}
}

func TestScopedReleases_NilSpec(t *testing.T) {
	if got := ScopedReleases([]forge.ReleaseInfo{rel("v1", false)}, nil, ""); got != nil {
		t.Fatalf("nil facet should return nil, got %v", got)
	}
}

func TestReleasesToPrune(t *testing.T) {
	mirror := []forge.ReleaseInfo{rel("v1", false), rel("v2", false), rel("old", false)}
	desired := []forge.ReleaseInfo{rel("v1", false), rel("v2", false)}
	got := ReleasesToPrune(mirror, desired)
	if len(got) != 1 || got[0] != "old" {
		t.Fatalf("prune should delete mirror-only releases, got %v", got)
	}
}
