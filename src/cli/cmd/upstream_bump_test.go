package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

func releasesOf(t *testing.T, raws ...string) []upstream.Release {
	sem, _ := scheme.Get(scheme.Semver)
	var out []upstream.Release
	for _, r := range raws {
		out = append(out, upstream.Release{Tag: "v" + r, Raw: r, Version: mustV(t, sem, r)})
	}
	return out
}

func TestPickTarget_Cooldown(t *testing.T) {
	newer := releasesOf(t, "2.22.0", "2.21.6")
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)

	if target, held := pickTarget(newer, nil, "core", 0, now, new(bool)); target.Raw != "2.22.0" || held != nil {
		t.Fatalf("no cooldown: the newest release")
	}
	lock := &upstream.Lock{}
	lock.Observe("core", []string{"2.21.6"}, now.Add(-96*time.Hour))
	changed := false
	target, held := pickTarget(newer, lock, "core", 72*time.Hour, now, &changed)
	if target == nil || target.Raw != "2.21.6" || held == nil || held.Raw != "2.22.0" || !changed {
		t.Fatalf("2.21.6 is old enough, 2.22.0 was just seen and is held: target=%v held=%v changed=%v", target, held, changed)
	}
}

func TestWithoutShadowed(t *testing.T) {
	discovered := []supplychain.Dependency{{Name: "ansible", File: "Dockerfile", Line: 2}, {Name: "alpine", File: "Dockerfile", Line: 1}}
	pins := []supplychain.Dependency{{Name: "core", File: "./Dockerfile", Line: 2}}
	if got := withoutShadowed(discovered, pins); len(got) != 1 || got[0].Name != "alpine" {
		t.Fatalf("got %+v", got)
	}
}

func TestAutoReleaseTag(t *testing.T) {
	root := t.TempDir()
	repo, _ := git.PlainInit(root, false)
	os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("ARG CORE=2.21.6\n"), 0o644)
	head := mkCommit(t, repo, "bump")
	repo.CreateTag("v2.21.5", head, nil)

	sem, _ := scheme.Get(scheme.Semver)
	src := upstream.Source{ID: "core", Kind: config.UpstreamKindGit, Location: "https://example.invalid/core", Select: upstream.Selection{Tags: regexp.MustCompile(config.DefaultUpstreamTags), Scheme: sem}}
	cs := config.UpstreamSource{ID: "core", Git: src.Location, Pins: []config.UpstreamPin{{File: "Dockerfile", Arg: "CORE"}}}
	seed := func(raws ...string) {
		listingMu.Lock()
		listings[src.Kind+" "+src.Location+" "+src.Select.Tags.String()] = upstreamListing{releases: releasesOf(t, raws...)}
		listingMu.Unlock()
	}

	seed("2.21.6", "2.21.5")
	if tag, reason := autoReleaseTag(t.Context(), root, cs, src); tag != "v2.21.6" {
		t.Fatalf("pins name an unreleased-by-us upstream release: tag=%q reason=%q", tag, reason)
	}
	repo.CreateTag("v2.21.6", head, nil)
	if tag, reason := autoReleaseTag(t.Context(), root, cs, src); tag != "" || reason != "" {
		t.Fatalf("already released: tag=%q reason=%q", tag, reason)
	}
	repo.DeleteTag("v2.21.6")
	seed("2.21.5")
	if tag, reason := autoReleaseTag(t.Context(), root, cs, src); tag != "" || reason == "" {
		t.Fatalf("pins naming a version upstream never released must not be tagged: tag=%q reason=%q", tag, reason)
	}
	_ = plumbing.ZeroHash
}
