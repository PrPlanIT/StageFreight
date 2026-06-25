package config

import "testing"

func TestTargetMatches(t *testing.T) {
	tagPol := map[string]string{"stable": `^v\d+\.\d+\.\d+$`}
	brPol := map[string]string{"main": `^main$`}

	dev := TargetConfig{When: TargetCondition{Branches: []string{"main"}, Events: []string{"push"}}}
	stable := TargetConfig{When: TargetCondition{GitTags: []string{"stable"}, Events: []string{"tag"}}}
	always := TargetConfig{}

	cases := []struct {
		name                string
		tgt                 TargetConfig
		event, branch, tag  string
		want                bool
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
	gitlabOnly := TargetConfig{When: TargetCondition{Forges: []string{"gitlab"}}}
	multi := TargetConfig{When: TargetCondition{Forges: []string{"github", "gitea"}}}
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
				t.Errorf("forge %q against forges:%v = %v, want %v", c.forge, c.tgt.When.Forges, got, c.want)
			}
		})
	}
}
