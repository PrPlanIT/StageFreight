package gitver

import "testing"

// TestSyntheticVersion_UsesCIEnv pins the dev-unknown fix: when there is no tag
// lineage, the synthetic version must carry the real CI commit SHA + branch so
// dev-{sha:8} resolves to the commit, not "dev-unknown".
func TestSyntheticVersion_UsesCIEnv(t *testing.T) {
	t.Setenv("SF_CI_SHA", "40c3334cdeadbeefcafe")
	t.Setenv("SF_CI_BRANCH", "main")
	t.Setenv("CI_COMMIT_SHA", "")

	v := SyntheticVersion()
	if v.SHA != "40c3334cdeadbeefcafe" {
		t.Errorf("SHA = %q, want the CI SHA", v.SHA)
	}
	if v.Branch != "main" {
		t.Errorf("Branch = %q, want main", v.Branch)
	}

	tags := ResolveTags([]string{"dev-{sha:8}"}, v)
	if len(tags) != 1 || tags[0] != "dev-40c3334c" {
		t.Errorf("ResolveTags = %v, want [dev-40c3334c]", tags)
	}
}

func TestSyntheticVersion_FallsBackToUnknown(t *testing.T) {
	for _, k := range []string{"SF_CI_SHA", "CI_COMMIT_SHA", "GITHUB_SHA", "SF_CI_BRANCH", "CI_COMMIT_BRANCH", "GITHUB_REF_NAME"} {
		t.Setenv(k, "")
	}
	v := SyntheticVersion()
	if v.SHA != "unknown" || v.Branch != "unknown" {
		t.Errorf("want unknown/unknown when no env, got %q/%q", v.SHA, v.Branch)
	}
}
