package config

import (
	"strings"
	"testing"
)

func TestFacetSpec_ReleaseInScope(t *testing.T) {
	sources := OrderedTagSources{
		{ID: "stable", Pattern: `^v\d+\.\d+\.\d+$`},
		{ID: "original", Pattern: `^(beta|proto)_v\d+\.\d+$`},
		{ID: "dev", Pattern: `^dev-`},
	}
	cases := []struct {
		spec FacetSpec
		tag  string
		want bool
	}{
		{FacetSpec{}, "dev-471b0c4", true},
		{FacetSpec{Only: []string{"stable", "original"}}, "v1.2.3", true},
		{FacetSpec{Only: []string{"stable", "original"}}, "beta_v0.614", true},
		{FacetSpec{Only: []string{"stable", "original"}}, "dev-471b0c4", false},
		{FacetSpec{Only: []string{"stable", "original"}}, "latest-dev", false},
		{FacetSpec{Match: "v*"}, "beta_v0.614", false},
		{FacetSpec{Match: "v*", Only: []string{"stable"}}, "v1.2.3", true},
	}
	for _, tc := range cases {
		if got := tc.spec.ReleaseInScope(tc.tag, sources); got != tc.want {
			t.Errorf("%+v %s = %v, want %v", tc.spec, tc.tag, got, tc.want)
		}
	}
}

// TestValidate_ReleaseScope: an unknown only: tag source or a bad match: glob fails at
// load, rather than scoping the mirror to nothing.
func TestValidate_ReleaseScope(t *testing.T) {
	cfg := &Config{Version: 1}
	cfg.Git.Tags = OrderedTagSources{{ID: "stable", Pattern: `^v\d+\.\d+\.\d+$`}}
	cfg.Repos = OrderedRepos{{ID: "github", Sync: SyncConfig{Releases: &FacetSpec{Scope: "all", Only: []string{"stabel"}, Match: "["}}}}
	_, err := Validate(cfg)
	if err == nil {
		t.Fatal("want validation errors")
	}
	for _, want := range []string{`repos.github.sync.releases.only: "stabel"`, "repos.github.sync.releases.match"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}
