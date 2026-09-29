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

// TestAutoInjectBuildArgs_Branch covers the BRANCH stamp: a real branch on branch
// builds, the primary repo's default branch as a fallback for tag/detached builds
// (where no branch exists), never fabricated, and never clobbering an explicit value.
func TestAutoInjectBuildArgs_Branch(t *testing.T) {
	const df = "gitops-server.dockerfile"
	det := &build.Detection{Dockerfiles: []build.DockerfileInfo{
		{Path: df, Args: []string{"VERSION", "COMMIT", "BUILD_DATE", "BRANCH"}},
	}}
	v := &build.VersionInfo{Version: "1.2.3", SHA: "abc1234"}

	t.Run("real CI branch is injected", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "")
		t.Setenv("CI_COMMIT_BRANCH", "main")
		t.Setenv("GITHUB_REF_NAME", "")
		got := autoInjectBuildArgs(map[string]string{}, det, v, df, "trunk")
		if got["BRANCH"] != "main" {
			t.Errorf("BRANCH = %q, want main (real branch, not the default fallback)", got["BRANCH"])
		}
	})

	t.Run("tag/detached build falls back to the default branch", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "")
		t.Setenv("CI_COMMIT_BRANCH", "") // tag pipeline: no branch set
		t.Setenv("GITHUB_REF_NAME", "")
		got := autoInjectBuildArgs(map[string]string{}, det, v, df, "main")
		if got["BRANCH"] != "main" {
			t.Errorf("BRANCH = %q, want main (default-branch fallback for a tag build)", got["BRANCH"])
		}
	})

	t.Run("never fabricated when neither a branch nor a default is known", func(t *testing.T) {
		t.Setenv("SF_CI_BRANCH", "")
		t.Setenv("CI_COMMIT_BRANCH", "")
		t.Setenv("GITHUB_REF_NAME", "")
		got := autoInjectBuildArgs(map[string]string{}, det, v, df, "")
		if _, ok := got["BRANCH"]; ok {
			t.Errorf("BRANCH must not be injected when unknown, got %q", got["BRANCH"])
		}
	})

	t.Run("explicit build-arg override is not clobbered", func(t *testing.T) {
		t.Setenv("CI_COMMIT_BRANCH", "main")
		got := autoInjectBuildArgs(map[string]string{"BRANCH": "custom"}, det, v, df, "main")
		if got["BRANCH"] != "custom" {
			t.Errorf("explicit BRANCH must win, got %q", got["BRANCH"])
		}
	})

	t.Run("not injected when the Dockerfile omits ARG BRANCH", func(t *testing.T) {
		t.Setenv("CI_COMMIT_BRANCH", "main")
		detNoArg := &build.Detection{Dockerfiles: []build.DockerfileInfo{
			{Path: df, Args: []string{"VERSION"}},
		}}
		got := autoInjectBuildArgs(map[string]string{}, detNoArg, v, df, "main")
		if _, ok := got["BRANCH"]; ok {
			t.Error("BRANCH must not be injected when the Dockerfile does not declare ARG BRANCH")
		}
	})
}
