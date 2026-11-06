package config

import "testing"

func TestTargetMatches(t *testing.T) {
	tagPol := map[string]string{"stable": `^v\d+\.\d+\.\d+$`}
	brPol := map[string]string{"main": `^main$`}

	dev := TargetConfig{When: WhenConditions{{Branches: []string{"main"}, Events: []string{"push"}}}}
	stable := TargetConfig{When: WhenConditions{{GitTags: []string{"stable"}, Events: []string{"tag"}}}}
	always := TargetConfig{}

	cases := []struct {
		name               string
		tgt                TargetConfig
		event, branch, tag string
		want               bool
	}{
		{"dev on main push", dev, "push", "main", "", true},
		{"dev on tag (wrong event)", dev, "tag", "", "v1.2.3", false},
		{"dev on other branch", dev, "push", "feature", "", false},
		{"stable on matching tag", stable, "tag", "", "v1.2.3", true},
		{"stable on push (wrong event)", stable, "push", "main", "", false},
		{"stable on non-matching tag", stable, "tag", "", "weird", false},
		{"no conditions matches on push", always, "push", "main", "", true},
		{"no conditions matches on tag", always, "tag", "", "v1.2.3", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TargetMatches(c.tgt, c.event, c.branch, c.tag, "", tagPol, brPol); got != c.want {
				t.Errorf("TargetMatches(event=%q branch=%q tag=%q) = %v, want %v",
					c.event, c.branch, c.tag, got, c.want)
			}
		})
	}
}

func TestTargetForges(t *testing.T) {
	gitlabOnly := TargetConfig{When: WhenConditions{{Forges: []string{"gitlab"}}}}
	multi := TargetConfig{When: WhenConditions{{Forges: []string{"github", "gitea"}}}}
	unrestricted := TargetConfig{} // no forges → eligible on every forge

	cases := []struct {
		name  string
		tgt   TargetConfig
		forge string
		want  bool
	}{
		{"gitlab-only on gitlab", gitlabOnly, "gitlab", true},
		{"gitlab-only on github", gitlabOnly, "github", false},
		{"multi on github", multi, "github", true},
		{"multi on gitea", multi, "gitea", true},
		{"multi on gitlab", multi, "gitlab", false},
		{"unrestricted on any forge", unrestricted, "github", true},
		{"unrestricted with empty forge", unrestricted, "", true},
		{"case-insensitive match", gitlabOnly, "GitLab", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Forge targets carry no event/branch/tag constraints, so those pass; the
			// forge dimension is the only gate under test.
			if got := TargetMatches(c.tgt, "push", "main", "", c.forge, nil, nil); got != c.want {
				t.Errorf("forge %q against %v = %v, want %v", c.forge, c.tgt.When, got, c.want)
			}
		})
	}
}

func TestTargetEligibilityGate(t *testing.T) {
	tagPolicies := map[string]string{"prerelease": `-rc`, "stable": `^v\d+\.\d+\.\d+$`}
	cases := []struct {
		name, event, branch, tag, want string
		when                           WhenConditions
	}{
		{"events", "push", "main", "", "events: tag", WhenConditions{{Events: []string{"tag"}, GitTags: []string{"stable"}}}},
		{"git_tags", "tag", "", "v1.2.3", "git_tags: prerelease", WhenConditions{{Events: []string{"tag"}, GitTags: []string{"prerelease"}}}},
		{"branches", "push", "dev", "", "branches: main", WhenConditions{{Branches: []string{"main"}}}},
		{"forges", "push", "main", "", "forges: github", WhenConditions{{Forges: []string{"github"}}}},
		{"several condition-sets", "push", "dev", "", "events: tag | branches: main",
			WhenConditions{{Events: []string{"tag"}}, {Branches: []string{"main"}, Events: []string{"push"}}}},
		{"repeated gate once", "push", "dev", "", "events: tag",
			WhenConditions{{Events: []string{"tag"}}, {Events: []string{"tag"}, GitTags: []string{"stable"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := TargetEligibility(TargetConfig{When: c.when}, c.event, c.branch, c.tag, "gitlab", tagPolicies, nil)
			if r.Eligible || r.Gate != c.want {
				t.Errorf("eligible=%v gate=%q, want gate %q", r.Eligible, r.Gate, c.want)
			}
		})
	}
}
