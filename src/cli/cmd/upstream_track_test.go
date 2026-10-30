package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

func trackCfg(track string) *config.Config {
	return &config.Config{
		Upstream: config.UpstreamConfig{
			Version: config.UpstreamVersion{Track: track},
			Sources: config.OrderedUpstreamSources{{
				ID:     "synapse",
				Image:  "ghcr.io/element-hq/synapse",
				Scheme: "semver",
				Select: config.UpstreamSelect{Tags: `^v(?P<version>\d+\.\d+\.\d+)$`},
			}},
		},
	}
}

// seedListing pre-populates the per-run listing cache so annotateTrackedBase resolves from
// these releases (newest first) instead of reaching the network.
func seedListing(t *testing.T, cfg *config.Config, raws ...string) {
	t.Helper()
	srcs, err := upstream.Sources(cfg.Upstream)
	if err != nil || len(srcs) == 0 {
		t.Fatalf("sources: %v", err)
	}
	key := listingKey(srcs[0])
	listingMu.Lock()
	listings[key] = upstreamListing{releases: releasesOf(t, raws...)}
	listingMu.Unlock()
	t.Cleanup(func() {
		listingMu.Lock()
		delete(listings, key)
		listingMu.Unlock()
	})
}

// stubDigest swaps the canonical digest resolver for a test double; the returned func restores it.
func stubDigest(t *testing.T, fn func(context.Context, string, string) (string, error)) func() {
	t.Helper()
	prev := resolveImageDigest
	resolveImageDigest = fn
	return func() { resolveImageDigest = prev }
}

func synapseBase(tag string) supplychain.Dependency {
	return supplychain.Dependency{
		Name:      "ghcr.io/element-hq/synapse:" + tag + "@sha256:abc",
		Current:   tag,
		Ecosystem: supplychain.EcosystemDockerImage,
		File:      "Dockerfile", Line: 1,
	}
}

func TestAnnotateTrackedBase(t *testing.T) {
	now := time.Now()

	t.Run("advances to the latest GA in the current major", func(t *testing.T) {
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "1.164.1", "1.163.0", "1.162.0")
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if !deps[0].TrackGoverned || deps[0].ResolvedTarget != "v1.164.1" {
			t.Fatalf("got governed=%v target=%q (want true, v1.164.1)", deps[0].TrackGoverned, deps[0].ResolvedTarget)
		}
	})

	t.Run("holds a newer major, advances within the current one", func(t *testing.T) {
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "2.0.0", "1.164.1", "1.162.0")
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "v1.164.1" {
			t.Fatalf("a new major must be held; target=%q (want v1.164.1)", deps[0].ResolvedTarget)
		}
	})

	t.Run("@major escape hatch adopts the pinned major", func(t *testing.T) {
		cfg := trackCfg("synapse@2")
		seedListing(t, cfg, "2.1.0", "2.0.0", "1.164.1")
		deps := []supplychain.Dependency{synapseBase("v1.164.1")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "v2.1.0" {
			t.Fatalf("track: synapse@2 must adopt 2.x; target=%q (want v2.1.0)", deps[0].ResolvedTarget)
		}
	})

	t.Run("already at the major's latest → no target, still governed", func(t *testing.T) {
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "1.164.1", "1.162.0")
		deps := []supplychain.Dependency{synapseBase("v1.164.1")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "" || !deps[0].TrackGoverned {
			t.Fatalf("got target=%q governed=%v (want \"\", true)", deps[0].ResolvedTarget, deps[0].TrackGoverned)
		}
	})

	t.Run("digest-pinned base re-pins to the new tag's digest", func(t *testing.T) {
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "1.164.1", "1.162.0")
		defer stubDigest(t, func(_ context.Context, image, tag string) (string, error) {
			if image != "ghcr.io/element-hq/synapse" || tag != "v1.164.1" {
				t.Fatalf("digest resolved for %s:%s", image, tag)
			}
			return "sha256:new164", nil
		})()
		dep := synapseBase("v1.162.0")
		dep.PinnedDigest = "sha256:old162"
		deps := []supplychain.Dependency{dep}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "v1.164.1" || deps[0].ResolvedDigest != "sha256:new164" {
			t.Fatalf("got target=%q digest=%q (want v1.164.1, sha256:new164)", deps[0].ResolvedTarget, deps[0].ResolvedDigest)
		}
	})

	t.Run("fail-closed: unresolved digest holds the bump (no corrupt pin)", func(t *testing.T) {
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "1.164.1", "1.162.0")
		defer stubDigest(t, func(context.Context, string, string) (string, error) {
			return "", context.DeadlineExceeded
		})()
		dep := synapseBase("v1.162.0")
		dep.PinnedDigest = "sha256:old162"
		deps := []supplychain.Dependency{dep}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "" || deps[0].ResolvedDigest != "" {
			t.Fatalf("an unresolved digest must hold the bump; got target=%q digest=%q", deps[0].ResolvedTarget, deps[0].ResolvedDigest)
		}
		if !deps[0].TrackGoverned {
			t.Fatalf("the base is still track-governed even when the bump is held")
		}
	})

	t.Run("a non-image track leaves the base untouched", func(t *testing.T) {
		cfg := trackCfg("synapse")
		cfg.Upstream.Sources[0].Image = ""
		cfg.Upstream.Sources[0].Git = "https://github.com/element-hq/synapse"
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].TrackGoverned || deps[0].ResolvedTarget != "" {
			t.Fatalf("a git-source track must not govern a base image; got governed=%v target=%q", deps[0].TrackGoverned, deps[0].ResolvedTarget)
		}
	})
}
