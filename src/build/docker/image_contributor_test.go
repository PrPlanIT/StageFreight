package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestImageContributorApplies pins the core fix: the perform spine now has a
// contributor for plain docker builds, and it never collides with crucible.
// The three valid shapes (binary-only, image-only, binary+image) and the
// crucible carve-out are all asserted here.
func TestImageContributorApplies(t *testing.T) {
	rc := func(builds ...config.BuildConfig) *domains.RunContext {
		return &domains.RunContext{Config: &config.Config{Builds: builds}}
	}
	plainDocker := config.BuildConfig{ID: "img", Kind: "docker"}
	crucible := config.BuildConfig{ID: "self", Kind: "docker", BuildMode: "crucible"}
	bin := config.BuildConfig{ID: "bin", Kind: "binary"}

	cases := []struct {
		name string
		rc   *domains.RunContext
		want bool
	}{
		{"image only (plain docker)", rc(plainDocker), true},
		{"binary + image", rc(plainDocker, bin), true},
		{"binary only", rc(bin), false},
		{"crucible only", rc(crucible), false},
		{"crucible + binary (no plain docker)", rc(crucible, bin), false},
	}
	c := &imageContributor{}
	for _, tc := range cases {
		if got := c.Applies(tc.rc); got != tc.want {
			t.Errorf("%s: Applies = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestImageContributorExcludesCrucibleFromPlan ensures a mixed config never
// feeds a crucible build to the image engine (that build belongs to
// crucibleContributor).
func TestImageContributorExcludesCrucibleFromPlan(t *testing.T) {
	rc := &domains.RunContext{Config: &config.Config{Builds: []config.BuildConfig{
		{ID: "img", Kind: "docker"},
		{ID: "self", Kind: "docker", BuildMode: "crucible"},
		{ID: "bin", Kind: "binary"},
	}}}
	got := (&imageContributor{}).plainDockerConfig(rc)
	if len(got.Builds) != 1 || got.Builds[0].ID != "img" {
		t.Fatalf("plainDockerConfig builds = %+v, want only [img]", got.Builds)
	}
}
