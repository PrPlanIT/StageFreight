package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestResolveBranch_HonorsCIEnvOverUnknown pins the 0-tags fix: when git is not
// available the version branch degrades to the synthetic "unknown", which must
// NOT win over a real CI-provided branch — otherwise branch-gated registry
// targets never match and the image builds but never publishes.
func TestResolveBranch_HonorsCIEnvOverUnknown(t *testing.T) {
	det := &build.Detection{} // no GitInfo (git unavailable)
	unknownV := &build.VersionInfo{Branch: "unknown"}

	t.Run("SF_CI_BRANCH beats synthetic unknown", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "main")
		t.Setenv("CI_COMMIT_BRANCH", "")
		t.Setenv("GITHUB_REF_NAME", "")
		if got := resolveBranch(det, unknownV); got != "main" {
			t.Errorf("resolveBranch = %q, want main", got)
		}
	})

	t.Run("SF_CI_BRANCH takes precedence over CI_COMMIT_BRANCH", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "feature")
		t.Setenv("CI_COMMIT_BRANCH", "main")
		if got := resolveBranch(det, unknownV); got != "feature" {
			t.Errorf("resolveBranch = %q, want feature", got)
		}
	})

	t.Run("unknown version branch is never returned", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "")
		t.Setenv("CI_COMMIT_BRANCH", "")
		t.Setenv("GITHUB_REF_NAME", "")
		if got := resolveBranch(det, unknownV); got != "" {
			t.Errorf("resolveBranch = %q, want empty (unknown must not win)", got)
		}
	})

	t.Run("real version branch is honored when no CI env", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "")
		t.Setenv("CI_COMMIT_BRANCH", "")
		t.Setenv("GITHUB_REF_NAME", "")
		if got := resolveBranch(det, &build.VersionInfo{Branch: "develop"}); got != "develop" {
			t.Errorf("resolveBranch = %q, want develop", got)
		}
	})
}
