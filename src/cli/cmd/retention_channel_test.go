package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// TestForgeStoreDelete_PruneTags pins that pruning a channel release removes the
// git tag too (no orphan), while a stable prune (pruneTags=false) removes only
// the release object — stable tags persist.
func TestForgeStoreDelete_PruneTags(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/repository/tags/"):
			calls = append(calls, "del-tag")
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/releases/"):
			calls = append(calls, "del-release")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fc := &forge.GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "g/p"}

	calls = nil
	if err := (&forgeStore{forge: fc}).Delete(context.Background(), "dev-abc"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "del-release" {
		t.Fatalf("pruneTags=false → %q, want del-release", got)
	}

	calls = nil
	if err := (&forgeStore{forge: fc, pruneTags: true}).Delete(context.Background(), "dev-abc"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "del-release,del-tag" {
		t.Fatalf("pruneTags=true → %q, want del-release,del-tag", got)
	}
}

// TestActiveReleaseTarget pins event-aware selection: a push selects the dev
// channel, a tag selects the stable target — even with both configured.
func TestActiveReleaseTarget(t *testing.T) {
	cfg := &config.Config{
		Matchers:   config.MatchersConfig{Branches: map[string]string{"main": `^main$`}},
		Versioning: config.VersioningConfig{TagSources: []config.TagSourceConfig{{ID: "stable", Pattern: `^v\d+\.\d+\.\d+$`}}},
		Targets: []config.TargetConfig{
			{ID: "stable", Kind: "release", Aliases: []string{"latest"},
				When: config.TargetCondition{GitTags: []string{"stable"}, Events: []string{"tag"}}},
			{ID: "dev", Kind: "release", Tag: "dev-{sha:8}", Aliases: []string{"latest-dev"},
				When: config.TargetCondition{Branches: []string{"main"}, Events: []string{"push"}}},
		},
	}
	t.Run("push selects dev", func(t *testing.T) {
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("CI_COMMIT_TAG", "")
		t.Setenv("SF_CI_EVENT", "push")
		t.Setenv("CI_COMMIT_BRANCH", "main")
		if got := activeReleaseTarget(cfg); got == nil || got.ID != "dev" {
			t.Fatalf("got %v, want dev", got)
		}
	})
	t.Run("tag selects stable", func(t *testing.T) {
		t.Setenv("SF_CI_TAG", "")
		t.Setenv("CI_COMMIT_TAG", "v1.2.3")
		t.Setenv("CI_COMMIT_BRANCH", "")
		if got := activeReleaseTarget(cfg); got == nil || got.ID != "stable" {
			t.Fatalf("got %v, want stable", got)
		}
	})
}
