package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// recordCommit swaps the commit step for one that records what it was asked to do, so
// the decisions can be asserted without driving git.
func recordCommit(t *testing.T) *[]commit.PlannerOptions {
	t.Helper()
	var calls []commit.PlannerOptions
	prev := presetRefreshCommit
	presetRefreshCommit = func(_ context.Context, _ *config.Config, _ string, o commit.PlannerOptions) (*commit.Result, error) {
		calls = append(calls, o)
		return &commit.Result{}, nil
	}
	t.Cleanup(func() { presetRefreshCommit = prev })
	return &calls
}

// repoWithCache is a workspace holding a retained preset cache, as a satellite has.
func repoWithCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cache := filepath.Join(dir, ".stagefreight", "preset-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "lint.yml"), []byte("lint:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cfgWith(refs ...string) *config.Config {
	outs := make([]presetref.Outcome, 0, len(refs))
	for _, r := range refs {
		outs = append(outs, presetref.Outcome{Ref: presetref.Parse(r), Fetched: true, Drifted: true, Refreshed: true})
	}
	c := &config.Config{}
	c.SetPresetOutcomes(outs)
	return c
}

func cfgWithOutcomes(outs ...presetref.Outcome) *config.Config {
	c := &config.Config{}
	c.SetPresetOutcomes(outs)
	return c
}

func TestRepublishRefreshedPresets(t *testing.T) {
	ctx := context.Background()
	ciCtx := &ci.CIContext{}

	// Nothing moved means nothing to record: a commit on every run would bury the ones
	// that mean something.
	t.Run("no drift commits nothing", func(t *testing.T) {
		calls := recordCommit(t)
		republishRefreshedPresets(ctx, cfgWith(), ciCtx, repoWithCache(t))
		if len(*calls) != 0 {
			t.Fatalf("committed with no drift: %+v", *calls)
		}
	})

	// A repo with no retained cache has nothing to carry back — committing would
	// create a cache it never had.
	t.Run("no cache in the repo commits nothing", func(t *testing.T) {
		calls := recordCommit(t)
		republishRefreshedPresets(ctx, cfgWith("https://a.example/x.yml"), ciCtx, t.TempDir())
		if len(*calls) != 0 {
			t.Fatalf("committed without a retained cache: %+v", *calls)
		}
	})

	// What gets carried back is what this run RETAINED, not what differed. A reference
	// governance never seeded is written without differing from anything; leaving it
	// uncommitted is what dirties the tree and aborts the dependency stage.
	t.Run("a first retention is carried back though nothing drifted", func(t *testing.T) {
		calls := recordCommit(t)
		republishRefreshedPresets(ctx, cfgWithOutcomes(presetref.Outcome{
			Ref: presetref.Parse("https://a.example/x.yml"), Fetched: true, Refreshed: true,
		}), ciCtx, repoWithCache(t))
		if len(*calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(*calls))
		}
	})

	// A pin that stopped matching writes nothing unless the operator chose to adopt the
	// source — so failing or keeping the retained copy must commit nothing, and adopting
	// must commit, because that run replaced what the repo holds.
	t.Run("a pin mismatch is carried back only when the source was adopted", func(t *testing.T) {
		ref := presetref.Parse("gitlab:Org/Repo//preset/x.yml@refs/tags/v1")

		calls := recordCommit(t)
		republishRefreshedPresets(ctx, cfgWithOutcomes(presetref.Outcome{
			Ref: ref, Fetched: true, Violated: true,
		}), ciCtx, repoWithCache(t))
		if len(*calls) != 0 {
			t.Fatalf("committed a pin mismatch that wrote nothing: %+v", *calls)
		}

		calls = recordCommit(t)
		republishRefreshedPresets(ctx, cfgWithOutcomes(presetref.Outcome{
			Ref: ref, Fetched: true, Violated: true, Drifted: true, Refreshed: true,
		}), ciCtx, repoWithCache(t))
		if len(*calls) != 1 {
			t.Fatalf("adopted pin not carried back: calls = %d, want 1", len(*calls))
		}
	})

	t.Run("drift commits the retained cache", func(t *testing.T) {
		calls := recordCommit(t)
		republishRefreshedPresets(ctx, cfgWith("https://b.example/y.yml", "https://a.example/x.yml"), ciCtx, repoWithCache(t))
		if len(*calls) != 1 {
			t.Fatalf("calls = %d, want 1", len(*calls))
		}
		o := (*calls)[0]

		// The cache is the whole payload: committing anything else would sweep
		// unrelated working-tree changes into an automated commit.
		if len(o.Paths) != 1 || o.Paths[0] != ".stagefreight/preset-cache" {
			t.Errorf("Paths = %v, want only the preset cache", o.Paths)
		}
		// The origin marker is what stops this commit re-triggering the pipeline that
		// produced it.
		if o.Origin != config.OriginGovernance {
			t.Errorf("Origin = %q, want %q", o.Origin, config.OriginGovernance)
		}
		if o.Type != "chore" || o.Scope != "presets" {
			t.Errorf("type/scope = %q/%q", o.Type, o.Scope)
		}
		// Every reference that moved must be named, or the commit says a refresh
		// happened without saying of what.
		for _, want := range []string{"https://a.example/x.yml", "https://b.example/y.yml"} {
			if !strings.Contains(o.Body, want) {
				t.Errorf("body does not name %q:\n%s", want, o.Body)
			}
		}
	})
}
