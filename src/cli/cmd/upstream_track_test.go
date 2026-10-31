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

// stubCreated swaps the canonical image-creation-time resolver for a test double; the returned
// func restores it.
func stubCreated(t *testing.T, fn func(context.Context, string, string) (time.Time, error)) func() {
	t.Helper()
	prev := resolveImageCreated
	resolveImageCreated = fn
	return func() { resolveImageCreated = prev }
}

// cooldown builds a follow cooldown of a single window (every size the same), as a maintainer's
// scalar min_release_age: 3d does.
func cooldown(d time.Duration) *supplychain.Cooldown {
	return &supplychain.Cooldown{Major: d, Minor: d, Patch: d}
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

	t.Run("a no-v base keeps its style but resolves the digest from upstream's real v-tag", func(t *testing.T) {
		// The real synapse case: FROM pins 1.162.0 (no v), while ghcr publishes only v-tags.
		// The written tag stays no-v (the maintainer's choice); the digest must come from the
		// tag the registry actually serves (v1.164.1), not the reconstructed 1.164.1.
		cfg := trackCfg("synapse")
		seedListing(t, cfg, "1.164.1", "1.162.0")
		defer stubDigest(t, func(_ context.Context, image, tag string) (string, error) {
			if tag != "v1.164.1" {
				t.Fatalf("digest must be resolved from upstream's real tag v1.164.1, got %s:%s", image, tag)
			}
			return "sha256:new164", nil
		})()
		dep := synapseBase("1.162.0") // no v
		dep.PinnedDigest = "sha256:old162"
		deps := []supplychain.Dependency{dep}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, nil)
		if deps[0].ResolvedTarget != "1.164.1" || deps[0].ResolvedDigest != "sha256:new164" {
			t.Fatalf("got target=%q digest=%q (want 1.164.1 — no-v style preserved — and sha256:new164)", deps[0].ResolvedTarget, deps[0].ResolvedDigest)
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

// TestAnnotateTrackedBase_FollowCooldown covers the follow cooldown: it ages a tracked base off
// the release's real image-creation time, honours the dedicated upstream.version.min_release_age
// knob over the general dependency cooldown, and falls back to the lock's first-sighting only for
// an upstream tag whose image carries no date.
func TestAnnotateTrackedBase_FollowCooldown(t *testing.T) {
	now := time.Now()

	t.Run("a release older than the follow window adopts on its real date", func(t *testing.T) {
		cfg := trackCfg("synapse")
		cfg.Upstream.Version.MinReleaseAge = cooldown(7 * 24 * time.Hour)
		seedListing(t, cfg, "1.164.1", "1.162.0")
		// Every tag's image was built a month ago — genuinely past the 7d window.
		defer stubCreated(t, func(_ context.Context, _, tag string) (time.Time, error) {
			return now.Add(-30 * 24 * time.Hour), nil
		})()
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, &upstream.Lock{})
		if deps[0].ResolvedTarget != "v1.164.1" {
			t.Fatalf("a month-old release must adopt despite a 7d follow window; target=%q (want v1.164.1)", deps[0].ResolvedTarget)
		}
	})

	t.Run("a release younger than the follow window is held", func(t *testing.T) {
		cfg := trackCfg("synapse")
		cfg.Upstream.Version.MinReleaseAge = cooldown(7 * 24 * time.Hour)
		seedListing(t, cfg, "1.164.1", "1.162.0")
		defer stubCreated(t, func(_ context.Context, _, tag string) (time.Time, error) {
			return now.Add(-24 * time.Hour), nil // a day old, inside the 7d window
		})()
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, &upstream.Lock{})
		if deps[0].ResolvedTarget != "" || deps[0].CooldownHeld != "v1.164.1" {
			t.Fatalf("a day-old release must be held by the 7d window; target=%q held=%q", deps[0].ResolvedTarget, deps[0].CooldownHeld)
		}
	})

	t.Run("the follow knob overrides the general dependency cooldown", func(t *testing.T) {
		cfg := trackCfg("synapse")
		cfg.Dependency.MinReleaseAge = cooldown(30 * 24 * time.Hour) // general policy would hold it
		cfg.Upstream.Version.MinReleaseAge = cooldown(time.Hour)     // but the fork chases upstream
		seedListing(t, cfg, "1.164.1", "1.162.0")
		defer stubCreated(t, func(_ context.Context, _, tag string) (time.Time, error) {
			return now.Add(-2 * time.Hour), nil
		})()
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, &upstream.Lock{})
		if deps[0].ResolvedTarget != "v1.164.1" {
			t.Fatalf("the dedicated follow knob (1h) must win over dependency.min_release_age (30d); target=%q", deps[0].ResolvedTarget)
		}
	})

	t.Run("an undated image falls back to the lock's first-sighting", func(t *testing.T) {
		cfg := trackCfg("synapse")
		cfg.Upstream.Version.MinReleaseAge = cooldown(7 * 24 * time.Hour)
		seedListing(t, cfg, "1.164.1", "1.162.0")
		// A reproducible-build image zeroes its creation time → no real date.
		defer stubCreated(t, func(context.Context, string, string) (time.Time, error) {
			return time.Time{}, nil
		})()
		// The lock already saw 1.164.1 a month ago, so the first-sighting fallback frees it.
		old := now.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
		lock := &upstream.Lock{Sources: []upstream.LockSource{{ID: "synapse", FirstSeen: map[string]string{"1.164.1": old}}}}
		deps := []supplychain.Dependency{synapseBase("v1.162.0")}
		annotateTrackedBase(context.Background(), cfg, t.TempDir(), deps, now, lock)
		if deps[0].ResolvedTarget != "v1.164.1" {
			t.Fatalf("an undated image must fall back to the lock's first-sighting; target=%q (want v1.164.1)", deps[0].ResolvedTarget)
		}
	})
}
