package pipeline

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/version"
)

// IdentityInfo must prefer the CI environment's short SHA and branch, so each
// CI job log is stamped with the real commit/branch.
func TestIdentityInfo_PrefersCIEnv(t *testing.T) {
	t.Setenv("CI_COMMIT_SHORT_SHA", "deadbee")
	t.Setenv("CI_COMMIT_BRANCH", "feature/x")

	info := IdentityInfo()
	if info.SHA != "deadbee" {
		t.Errorf("SHA = %q, want CI short sha %q", info.SHA, "deadbee")
	}
	if info.Branch != "feature/x" {
		t.Errorf("Branch = %q, want CI branch %q", info.Branch, "feature/x")
	}
	if info.Version != version.Version {
		t.Errorf("Version = %q, want build-time %q", info.Version, version.Version)
	}
}

// A tag event (no branch) must surface the tag as the branch slot.
func TestIdentityInfo_FallsBackToTag(t *testing.T) {
	t.Setenv("CI_COMMIT_SHORT_SHA", "")
	t.Setenv("CI_COMMIT_SHA", "0123456789abcdef")
	t.Setenv("CI_COMMIT_BRANCH", "")
	t.Setenv("CI_COMMIT_TAG", "v1.0.0")

	info := IdentityInfo()
	if info.SHA != "01234567" {
		t.Errorf("SHA = %q, want truncated full sha %q", info.SHA, "01234567")
	}
	if info.Branch != "v1.0.0" {
		t.Errorf("Branch = %q, want tag %q", info.Branch, "v1.0.0")
	}
}

// Standalone/local runs (no CI env) must fall back to the build-time commit so
// the identity is never blank.
func TestIdentityInfo_FallsBackToBuildCommit(t *testing.T) {
	t.Setenv("CI_COMMIT_SHORT_SHA", "")
	t.Setenv("CI_COMMIT_SHA", "")
	t.Setenv("CI_COMMIT_BRANCH", "")
	t.Setenv("CI_COMMIT_TAG", "")

	info := IdentityInfo()
	if info.SHA != version.Commit {
		t.Errorf("SHA = %q, want build-time commit %q", info.SHA, version.Commit)
	}
	if info.Branch != "" {
		t.Errorf("Branch = %q, want empty for local run", info.Branch)
	}
}
