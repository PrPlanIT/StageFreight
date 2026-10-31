package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// annotateTrackedBase makes upstream.version.track the authority for the fork's base image.
// When track names an image source, the Dockerfile FROM for that image IS the tracked
// upstream: it advances to upstream's latest GA within the tracked major (the base's own
// major, or the major pinned by track: <source>@<major>), bypassing max_update — which
// scopes ordinary dependencies, not the version the fork's release claims. A newer major is
// left for review (it surfaces as MajorAvailable); the escape hatch to adopt it is the
// @<major> line pin.
//
// The target is cooldown-gated by the same min_release_age lock the declared pins use: image
// registries carry no reliable per-tag publish time, so a tag's first sighting is recorded in
// .stagefreight/upstream.lock and the tag is adopted only once it has aged. The matched
// dependency is mutated in place; lockChanged reports that the lock needs writing (the
// caller's finish does so, riding the same commit). A non-image track, an unparseable base,
// or no matching FROM leaves everything untouched.
func annotateTrackedBase(ctx context.Context, appCfg *config.Config, rootDir string, deps []supplychain.Dependency, now time.Time, lock *upstream.Lock) (lockChanged bool) {
	mode, id := appCfg.Upstream.Version.Mode()
	if mode != config.UpstreamVersionTrack {
		return false
	}
	sources, err := upstream.Sources(appCfg.Upstream)
	if err != nil {
		return false
	}
	var src upstream.Source
	for _, s := range sources {
		if s.ID == id {
			src = s
		}
	}
	if src.ID == "" || src.Kind != config.UpstreamKindImage {
		// track on a non-image source governs only the release claim, not a base image.
		return false
	}

	// The discovered FROM dependency for the tracked image. Its Name is the full reference
	// (host/repo:tag@sha256:…); the repository alone must equal the source's image.
	var dep *supplychain.Dependency
	for i := range deps {
		if deps[i].Ecosystem == supplychain.EcosystemDockerImage && imageRepo(deps[i].Name) == src.Location {
			dep = &deps[i]
			break
		}
	}
	if dep == nil {
		return false
	}

	cur, err := src.Select.Scheme.Parse(strings.TrimPrefix(dep.Current, "v"))
	if err != nil {
		fmt.Printf("  upstream: %s base %q is not a %s version; not track-governed\n", src.ID, dep.Current, src.Select.Scheme.Name())
		return false
	}
	major, pinned := appCfg.Upstream.Version.TrackLine()
	if !pinned {
		m, ok := leadingMajor(dep.Current)
		if !ok {
			fmt.Printf("  upstream: %s base %q has no readable major line; not track-governed\n", src.ID, dep.Current)
			return false
		}
		major = m
	}
	l := listUpstream(ctx, src)
	if l.err != nil {
		fmt.Printf("  upstream: %s base not advanced: %v\n", src, l.err)
		return false
	}

	// Releases strictly newer than the base, within the tracked major (newest first).
	var newer []upstream.Release
	for _, r := range l.releases {
		if m, ok := leadingMajor(r.Raw); !ok || m != major {
			continue
		}
		if r.Version.Compare(cur) > 0 {
			newer = append(newer, r)
		}
	}

	// The base is the authority — never capped by max_update — but paced by the follow
	// cooldown. When a cooldown applies, date each candidate by its own image's creation time
	// so the follow ages from the release's real publish time (an upstream tag already weeks
	// old is adopted at once), not from when StageFreight first saw the tag; an image that
	// zeroes that field falls back to the lock's first-sighting inside pickTarget. With no
	// cooldown the dates are irrelevant, so the network lookups are skipped entirely.
	dep.TrackGoverned = true
	age := trackReleaseAge(appCfg)
	if age > 0 {
		dateTrackCandidates(ctx, src.Location, newer)
	}
	target, held := pickTarget(newer, lock, src.ID, age, now, &lockChanged)
	switch {
	case target != nil:
		newTag := upstream.RepinValue(dep.Current, target.Raw)
		// A digest-pinned base must re-pin to the NEW tag's digest from the image's own
		// registry. Fail closed: if the digest can't be resolved, do not bump — a tag-only
		// bump would leave the old digest, writing a corrupt new-tag@old-digest pin. The
		// next run retries.
		if dep.PinnedDigest != "" {
			digest, err := resolveImageDigest(ctx, src.Location, newTag)
			if err != nil {
				fmt.Printf("  upstream: %s base %s → %s held: could not resolve the new tag's digest: %v\n", src.ID, dep.Current, newTag, err)
				return lockChanged
			}
			dep.ResolvedDigest = digest
		}
		dep.ResolvedTarget = newTag
		fmt.Printf("  upstream: %s base %s → %s (upstream latest in %d.x)\n", src.ID, dep.Current, newTag, major)
	case held != nil:
		dep.CooldownHeld = upstream.RepinValue(dep.Current, held.Raw)
		fmt.Printf("  upstream: %s base %s held %s by the follow cooldown\n", src.ID, held.Raw, age)
	}
	return lockChanged
}

// trackReleaseAge is how old an upstream GA must be before the fork follows it onto the base
// image: the dedicated upstream.version.min_release_age when set, else the general
// dependency.min_release_age. Zero means follow the instant upstream publishes.
func trackReleaseAge(appCfg *config.Config) time.Duration {
	if a := appCfg.Upstream.Version.MinReleaseAge; a != nil {
		return a.Longest()
	}
	if a := appCfg.Dependency.MinReleaseAge; a != nil {
		return a.Longest()
	}
	return 0
}

// dateTrackCandidates stamps each still-undated release with its image's creation time from the
// tracked image's own registry, so the follow cooldown ages from the release's real publish
// time. A lookup that fails, or an image that zeroes the field (a reproducible build, at or
// before the Unix epoch), leaves the candidate undated — pickTarget then falls back to the
// lock's first-sighting for it.
func dateTrackCandidates(ctx context.Context, image string, rels []upstream.Release) {
	for i := range rels {
		if !rels[i].Published.IsZero() {
			continue
		}
		t, err := resolveImageCreated(ctx, image, rels[i].Tag)
		if err != nil || t.Unix() <= 0 {
			continue
		}
		rels[i].Published = t
	}
}

// resolveImageDigest resolves a tag's manifest digest from the image's canonical registry.
// A package var so tests can stub the network.
var resolveImageDigest = discovery.ManifestDigestCanonical

// resolveImageCreated resolves a tag's image creation time from the image's canonical registry,
// the follow cooldown's real-date clock. A package var so tests can stub the network.
var resolveImageCreated = discovery.ManifestCreatedCanonical

// imageRepo returns the repository of a Docker image reference — the Name with any tag and
// digest removed (host/path) — the form an upstream.sources image is declared as.
func imageRepo(name string) string {
	ref, _ := discovery.SplitImageDigest(name)
	repo, _ := discovery.SplitImageTag(ref)
	return repo
}

// leadingMajor reads the leading integer of a version string (an optional v stripped),
// e.g. "v1.164.1" → 1. ok is false when there is no leading digit.
func leadingMajor(s string) (int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}
