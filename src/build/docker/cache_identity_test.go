package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// Planning a build and retaining its cache must agree on the repo, in CI and out.
func TestCacheIdentityIsOneRepoIdentity(t *testing.T) {
	t.Setenv("CI_PROJECT_PATH", "Group/repo")
	t.Setenv("SF_CI_REPO_URL", "https://forge.example/Group/repo")
	det := &build.Detection{RootDir: "/src"}
	cfg := &config.Config{}

	plan := resolveRepoID(cfg, det)
	retain := resolveRepoIDFromContext(&pipeline.PipelineContext{Config: cfg})
	if plan != retain {
		t.Errorf("plan scopes the cache to %q, retention to %q", plan, retain)
	}
	if repoHash(plan) != repoHash(retain) {
		t.Errorf("cache keys differ: %s vs %s", repoHash(plan), repoHash(retain))
	}
}
