package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
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
func annotateTrackedBase(ctx context.Context, appCfg *config.Config, rootDir string, deps []supplychain.Dependency, now time.Time, lock *upstream.Lock, rows *upstreamRows) (lockChanged bool) {
	mode, id := appCfg.Upstream.Version.Mode()
	if mode != config.UpstreamVersionTrack {
		return false
	}
	sources, err := upstream.Sources(appCfg.Upstream, rootDir)
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
		rows.add(src.ID, "base %q is not a %s version; not track-governed", dep.Current, src.Select.Scheme.Name())
		return false
	}
	major, pinned := appCfg.Upstream.Version.TrackLine()
	if !pinned {
		m, ok := leadingMajor(dep.Current)
		if !ok {
			rows.add(src.ID, "base %q has no readable major line; not track-governed", dep.Current)
			return false
		}
		major = m
	}
	l := listUpstream(ctx, src)
	if l.err != nil {
		rows.add(src.ID, "base not advanced: %v", l.err)
		return false
	}

	// Reachability (read-only, empirical): the tag the maintainer pinned on the FROM must be a
	// tag this registry actually serves. A digest-pinned base still builds when its tag string
	// 404s — the digest carries the pull — so this never blocks; but a pinned tag the registry
	// does not have is a dead reference that any tag-resolving tool trips over, hidden behind the
	// digest. Surface it. No style is asserted: the registry is the sole authority on which tag
	// strings exist, so the check asks it directly, and when the pin is absent it names the form
	// the registry does serve for the same version — evidence, not a prescribed convention.
	if w := baseTagWarning(ctx, src, dep.Current, cur, l.releases); w != "" {
		rows.add(src.ID, "%s", w)
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
	target, held := pickTarget(newer, lock, src.ID, age, now, &lockChanged, rows)
	switch {
	case target != nil:
		// Write the tag in the maintainer's own style: RepinValue keeps a leading v, or its
		// absence, exactly as the FROM pins it — both are valid and the fork chooses, so the
		// engine never rewrites one into the other. The DIGEST, though, is resolved from
		// upstream's ACTUAL tag (target.Tag), which is what the registry serves: a FROM pinned
		// without the v still re-pins to the real (v-tagged) manifest's digest, not to a
		// reconstructed tag that may not exist on the registry at all.
		newTag := upstream.RepinValue(dep.Current, target.Raw)
		if !retargetBase(ctx, src, dep, target, newTag, rows) {
			return lockChanged
		}
		rows.add(src.ID, "base %s → %s (upstream latest in %d.x)", dep.Current, newTag, major)
	case held != nil:
		dep.CooldownHeld = upstream.RepinValue(dep.Current, held.Raw)
		rows.add(src.ID, "base %s held %s by the follow cooldown", held.Raw, age)
	}
	return lockChanged
}

// retargetBase points a base image at a release. Fail closed: without the digest of a digest-
// pinned base it does not bump, because a tag-only bump would write a corrupt new-tag@old-digest
// pin. The next run retries.
func retargetBase(ctx context.Context, src upstream.Source, dep *supplychain.Dependency, target *upstream.Release, newTag string, rows *upstreamRows) bool {
	if dep.PinnedDigest != "" {
		digest, err := resolveImageDigest(ctx, src.Location, target.Tag)
		if err != nil {
			rows.add(src.ID, "base %s → %s held: could not resolve %s's digest: %v", dep.Current, newTag, target.Tag, err)
			return false
		}
		dep.ResolvedDigest = digest
	}
	dep.ResolvedTarget = newTag
	return true
}

// alpineBaseSource lists Alpine's releases for a followed base.
func alpineBaseSource(id string) upstream.Source {
	sem, _ := scheme.Get(scheme.Semver)
	return upstream.Source{ID: id + "-alpine", Kind: config.UpstreamKindImage, Location: "docker.io/library/alpine",
		Select: upstream.Selection{Tags: alpineReleaseTag, Scheme: sem}}
}

var alpineReleaseTag = regexp.MustCompile(`^(?P<version>\d+\.\d+\.\d+)$`)

// followAlpineBases moves the Alpine FROM of every repository source to Alpine's newest release
// in its major, paced by the follow cooldown. It returns the suite of each source whose release
// line changes, for its pins to move with it. A line that lacks the package, or holds an older
// version than the pins, is not adopted.
func followAlpineBases(ctx context.Context, appCfg *config.Config, rootDir string, deps []supplychain.Dependency, now time.Time, lock *upstream.Lock, rows *upstreamRows) (moved map[string]string, lockChanged bool) {
	moved = map[string]string{}
	sources, err := upstream.Sources(appCfg.Upstream, rootDir)
	if err != nil {
		return moved, false
	}
	for i, cs := range appCfg.Upstream.Sources {
		if cs.Repository == "" {
			continue
		}
		src := sources[i]
		dep := finalAlpineBase(deps, cs.Pins[0].File)
		if dep == nil {
			rows.add(cs.ID, "no pinned alpine FROM in %s; release not followed", cs.Pins[0].File)
			continue
		}
		base := alpineBaseSource(cs.ID)
		major, ok := leadingMajor(dep.Current)
		cur, err := base.Select.Scheme.Parse(dep.Current)
		if !ok || err != nil {
			rows.add(cs.ID, "base %q is not an Alpine version; release not followed", dep.Current)
			continue
		}
		l := listUpstream(ctx, base)
		if l.err != nil {
			rows.add(cs.ID, "base not advanced: %v", l.err)
			continue
		}
		var newer []upstream.Release
		for _, r := range l.releases {
			if m, ok := leadingMajor(r.Raw); ok && m == major && r.Version.Compare(cur) > 0 {
				newer = append(newer, r)
			}
		}
		age := trackReleaseAge(appCfg)
		if age > 0 {
			dateTrackCandidates(ctx, base.Location, newer)
		}
		// Governed from here: the ceiling does not cap it, so a base with no adoptable release
		// stays where it is rather than taking the natural target.
		dep.TrackGoverned = true
		dep.ResolvedTarget = dep.Current
		for len(newer) > 0 {
			target, held := pickTarget(newer, lock, base.ID, age, now, &lockChanged, rows)
			if target == nil {
				if held != nil {
					dep.CooldownHeld = upstream.RepinValue(dep.Current, held.Raw)
				}
				break
			}
			suite := upstream.AlpineSuite(target.Raw, cs.Repository)
			if suite != src.Suite {
				if reason := suiteUnusable(ctx, rootDir, cs, src, suite); reason != "" {
					rows.add(cs.ID, "base stays off %s: %s", suite, reason)
					newer = withoutSuite(newer, suite, cs.Repository)
					continue
				}
			}
			newTag := upstream.RepinValue(dep.Current, target.Raw)
			if !retargetBase(ctx, base, dep, target, newTag, rows) {
				break
			}
			if suite != src.Suite {
				moved[cs.ID] = suite
			}
			rows.add(cs.ID, "base %s → %s (%s)", dep.Current, newTag, suite)
			break
		}
	}
	return moved, lockChanged
}

// finalAlpineBase is the discovered alpine FROM of the file's last stage that has one.
func finalAlpineBase(deps []supplychain.Dependency, file string) *supplychain.Dependency {
	var dep *supplychain.Dependency
	for i := range deps {
		d := &deps[i]
		if d.Ecosystem == supplychain.EcosystemDockerImage && filepath.Clean(d.File) == filepath.Clean(file) &&
			upstream.IsAlpineImage(imageRepo(d.Name)) && (dep == nil || d.Line > dep.Line) {
			dep = d
		}
	}
	return dep
}

// suiteUnusable says why a source's pins could not move to suite, or "" when they can.
func suiteUnusable(ctx context.Context, rootDir string, cs config.UpstreamSource, src upstream.Source, suite string) string {
	_, current, err := readPins(rootDir, cs, src.Select.Scheme)
	if err != nil {
		return err.Error()
	}
	src.Suite = suite
	l := listUpstream(ctx, src)
	switch {
	case l.err != nil:
		return l.err.Error()
	case len(l.releases) == 0:
		return "it has no " + src.Location + " package"
	case l.releases[0].Version.Compare(current) < 0:
		return fmt.Sprintf("its %s %s is older than the pinned %s", src.Location, l.releases[0].Raw, current)
	}
	return ""
}

func withoutSuite(rels []upstream.Release, suite, repository string) []upstream.Release {
	var out []upstream.Release
	for _, r := range rels {
		if upstream.AlpineSuite(r.Raw, repository) != suite {
			out = append(out, r)
		}
	}
	return out
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

// baseTagWarning returns the warning for a tracked base whose pinned FROM tag is not a tag its
// registry serves, or "" when the pin resolves. It asks the registry whether that exact string
// resolves; a 404 is the registry's definite "I do not have this", and only then is there a
// warning — a transient failure is not evidence a pin is dead and is passed over in silence (the
// listing step already reports an unreachable registry). When the pin is dead it names the form
// the registry does serve for the same version, read from the listing already in hand: data,
// never a v-or-no-v rule. Returning the message rather than printing it keeps the check unit-
// testable; the caller adds it to the Upstream section so the audition log carries it loudly.
func baseTagWarning(ctx context.Context, src upstream.Source, pinned string, v scheme.Version, releases []upstream.Release) string {
	_, err := resolveImageDigest(ctx, src.Location, pinned)
	if err == nil || !discovery.IsManifestNotFound(err) {
		return ""
	}
	if served := servedTagFor(releases, v); served != "" {
		return fmt.Sprintf("base tag %q is a dead reference — %s does not serve it; it serves this version as %q (the pin builds by digest, but its tag resolves to nothing)", pinned, src.Location, served)
	}
	return fmt.Sprintf("base tag %q is a dead reference — %s does not serve it (the pin builds by digest, but its tag resolves to nothing)", pinned, src.Location)
}

// servedTagFor returns the literal tag the registry serves for exactly this version, read from
// the listing already fetched, or "" when none matches — pure data, no network and no style rule.
func servedTagFor(releases []upstream.Release, v scheme.Version) string {
	for i := range releases {
		if releases[i].Version.Compare(v) == 0 {
			return releases[i].Tag
		}
	}
	return ""
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
