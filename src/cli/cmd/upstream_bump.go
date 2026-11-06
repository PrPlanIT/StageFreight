package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/dependency"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// upstreamListing is one source's listing, shared by every consumer in the run.
type upstreamListing struct {
	releases []upstream.Release
	unparsed []string
	commits  map[string]string // every listed tag → commit, selected or not
	err      error
}

var (
	listingMu sync.Mutex
	listings  = map[string]upstreamListing{}
)

// listUpstream lists a source once per run: the watcher, the pin bump and auto-release all
// read the same answer, and the upstream is asked only once.
func listUpstream(ctx context.Context, src upstream.Source) upstreamListing {
	key := listingKey(src)
	listingMu.Lock()
	defer listingMu.Unlock()
	if l, ok := listings[key]; ok {
		return l
	}
	lctx, cancel := context.WithTimeout(ctx, upstreamListTimeout)
	defer cancel()
	var l upstreamListing
	if cands, err := upstream.List(lctx, src); err != nil {
		l.err = err
	} else {
		l.releases, l.unparsed = upstream.ReleasesOf(cands, src.Select)
		l.commits = make(map[string]string, len(cands))
		for _, c := range cands {
			l.commits[c.Name] = c.Commit
		}
	}
	listings[key] = l
	return l
}

// listingKey identifies one listing: the same upstream read with the same selection.
func listingKey(src upstream.Source) string {
	return src.Kind + " " + src.Location + " " + src.Suite + " " + src.Select.Tags.String()
}

// readPins locates every pin of a source and checks they agree. current is the version they
// all hold.
func readPins(rootDir string, cs config.UpstreamSource, sch scheme.Scheme) (pins []upstream.PinValue, current scheme.Version, err error) {
	for _, p := range cs.Pins {
		pv, err := upstream.LocatePin(rootDir, cs.ID, p)
		if err != nil {
			return nil, nil, err
		}
		v, err := sch.Parse(pv.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("%s holds %q, which is not a %s version", pv.Describe(), pv.Value, sch.Name())
		}
		if current != nil && v.Compare(current) != 0 {
			return nil, nil, fmt.Errorf("pins disagree: %s holds %s, %s holds %s — fix them to one version so they can move together", pins[0].Describe(), pins[0].Value, pv.Describe(), pv.Value)
		}
		current = v
		pins = append(pins, pv)
	}
	return pins, current, nil
}

// upstreamPinDeps turns every declared pin into a dependency of the upstream ecosystem, so
// the dependency engine moves it with its usual max_update, commit and MR handling. With
// dependency.min_release_age set, a release is adopted only once it has been seen for that
// long: the first sighting is recorded in .stagefreight/upstream.lock (the caller writes it)
// because git tags carry no release time. The tracked source's pins are the fork's release, so
// max_update does not cap them; they stay within the tracked major. moved names the sources
// whose Alpine base moves to a new suite this run: their pins move to it with no cooldown of
// their own, because the old version is not in the new suite.
//
// afterBump holds, per source with a target, the versions still newer than that target: once
// the bump applies, the lock keeps only those, so it never records a version our pins already
// hold.
func upstreamPinDeps(ctx context.Context, appCfg *config.Config, rootDir string, now time.Time, lock *upstream.Lock, moved map[string]string) (deps []supplychain.Dependency, lockChanged bool, afterBump map[string][]string) {
	afterBump = map[string][]string{}
	var rows [][2]string // (label, text) rows for the Upstream section, rendered once at the end
	u := appCfg.Upstream
	sources, err := upstream.Sources(u, rootDir)
	if err != nil {
		return nil, false, nil
	}
	mode, trackedID := u.Version.Mode()
	var age time.Duration
	if appCfg.Dependency.MinReleaseAge != nil {
		age = appCfg.Dependency.MinReleaseAge.Longest()
	}
	for i, cs := range u.Sources {
		if len(cs.Pins) == 0 {
			continue
		}
		src := sources[i]
		suite, suiteMoved := moved[cs.ID]
		if suiteMoved {
			src.Suite = suite
		}
		pins, current, err := readPins(rootDir, cs, src.Select.Scheme)
		if err != nil {
			rows = append(rows, [2]string{cs.ID, "pins not moved: " + err.Error()})
			continue
		}
		l := listUpstream(ctx, src)
		if l.err != nil {
			rows = append(rows, [2]string{cs.ID, "pins not moved: " + l.err.Error()})
			continue
		}
		tracked := mode == config.UpstreamVersionTrack && cs.ID == trackedID
		line, lineKnown := trackedMajor(appCfg, pins[0].Value)
		var newer []upstream.Release
		for _, r := range l.releases {
			if tracked && lineKnown {
				if m, ok := leadingMajor(r.Raw); !ok || m != line {
					continue
				}
			}
			if r.Version.Compare(current) > 0 {
				newer = append(newer, r)
			}
		}
		var target, held *upstream.Release
		if suiteMoved {
			if len(newer) > 0 {
				target = &newer[0]
			}
		} else {
			target, held = pickTarget(newer, lock, cs.ID, age, now, &lockChanged)
		}
		if target != nil {
			for _, r := range newer {
				if r.Version.Compare(target.Version) > 0 {
					afterBump[cs.ID] = append(afterBump[cs.ID], r.Raw)
				}
			}
		}
		var available []string
		for _, r := range l.releases {
			available = append(available, r.Raw)
		}
		for _, pv := range pins {
			d := supplychain.Dependency{
				Name: cs.ID + " · " + pv.Describe(), Group: cs.ID, Ecosystem: supplychain.EcosystemUpstream,
				Current: pv.Value, Latest: pv.Value,
				File: pv.File, Line: pv.Line, Column: pv.Column,
				Binding: pv.Describe(), SourceURL: src.Location,
				TrackGoverned: tracked,
			}
			if target != nil {
				d.Latest = upstream.RepinValue(pv.Value, target.Raw)
			}
			if held != nil {
				d.CooldownHeld = upstream.RepinValue(pv.Value, held.Raw)
			}
			for _, a := range available {
				d.AvailableVersions = append(d.AvailableVersions, upstream.RepinValue(pv.Value, a))
			}
			deps = append(deps, d)
		}
		switch {
		case target != nil:
			rows = append(rows, [2]string{cs.ID, fmt.Sprintf("%s → %s  (bump)", current, target.Raw)})
		case held != nil:
			rows = append(rows, [2]string{cs.ID, fmt.Sprintf("hold %s — within min_release_age (%s)", held.Raw, appCfg.Dependency.MinReleaseAge)})
		default:
			rows = append(rows, [2]string{cs.ID, fmt.Sprintf("current %s — no newer release", current)})
		}
	}
	if len(rows) > 0 {
		sec := upstreamSection()
		for _, r := range rows {
			sec.Row("%-14s%s", r[0], r[1])
		}
		sec.Close()
	}
	return deps, lockChanged, afterBump
}

// trackedMajor is the major a tracked source stays within: upstream.version.track's @major, else
// the current value's.
func trackedMajor(appCfg *config.Config, current string) (int, bool) {
	if major, pinned := appCfg.Upstream.Version.TrackLine(); pinned {
		return major, true
	}
	return leadingMajor(current)
}

// pickTarget returns the newest release old enough to adopt, and the newest one the cooldown
// is still holding. newer is newest first.
func pickTarget(newer []upstream.Release, lock *upstream.Lock, id string, age time.Duration, now time.Time, changed *bool) (target, held *upstream.Release) {
	if age <= 0 || lock == nil {
		if len(newer) > 0 {
			return &newer[0], nil
		}
		return nil, nil
	}
	// A release the upstream timestamps is aged from that; only the rest need the lock.
	var raws []string
	for _, r := range newer {
		if r.Published.IsZero() {
			raws = append(raws, r.Raw)
		}
	}
	seen, healed, ch := lock.Observe(id, raws, now)
	*changed = *changed || ch
	if len(healed) > 0 {
		fmt.Printf("  upstream: %s reset corrupt cooldown timestamp(s) in the lock: %v\n", id, healed)
	}
	for i := range newer {
		at := newer[i].Published
		if at.IsZero() {
			at = seen[newer[i].Raw]
		}
		if now.Sub(at) >= age {
			return &newer[i], held
		}
		if held == nil {
			held = &newer[i]
		}
	}
	return nil, held
}

// withoutShadowed drops discovered dependencies that sit on the same line as a declared
// upstream pin: the declaration is the authority for that value, and two writers must never
// edit one line.
func withoutShadowed(discovered, pins []supplychain.Dependency) []supplychain.Dependency {
	at := map[string]bool{}
	for _, p := range pins {
		at[fmt.Sprintf("%s:%d", filepath.Clean(p.File), p.Line)] = true
	}
	out := discovered[:0:0]
	for _, d := range discovered {
		if !at[fmt.Sprintf("%s:%d", filepath.Clean(d.File), d.Line)] {
			out = append(out, d)
		}
	}
	return out
}

// lockRel is the lock's repo-relative path, as the commit machinery lists changed files.
func lockRel() string { return strings.TrimPrefix(upstream.LockPath(""), "/") }

// joinUpstreamPins is how every dependency-update entry point (the CI audition and the
// `dependency update` command) takes part in upstream mode: declared pins join the
// discovered dependencies, an ecosystem filter is widened to admit them (scope narrows
// discovery; a declared pin is never out of scope), and finish writes the cooldown lock after
// the update — the clean-tree check precedes it — so it rides the same commit. Evaluate-only
// runs write nothing.
func joinUpstreamPins(ctx context.Context, appCfg *config.Config, rootDir string, deps []supplychain.Dependency, ecosystems []string) ([]supplychain.Dependency, []string, func(*dependency.UpdateResult, bool)) {
	now := time.Now()
	// The lock is the first-sighting clock for pins (dependency.min_release_age) and for a
	// followed base image (upstream.version.min_release_age), whichever is set.
	var lock *upstream.Lock
	if appCfg.Dependency.MinReleaseAge != nil || trackReleaseAge(appCfg) > 0 {
		var err error
		if lock, err = upstream.ReadLock(rootDir); err != nil {
			fmt.Printf("  upstream: cannot read %s, cooldown starts over: %v\n", upstream.LockPath(""), err)
			lock = &upstream.Lock{}
		}
	}
	// Bases are annotated in place, before withoutShadowed copies the discovered deps.
	moved, lockChanged := followAlpineBases(ctx, appCfg, rootDir, deps, now, lock)
	upDeps, pinsChanged, afterBump := upstreamPinDeps(ctx, appCfg, rootDir, now, lock, moved)
	lockChanged = lockChanged || pinsChanged
	if annotateTrackedBase(ctx, appCfg, rootDir, deps, now, lock) {
		lockChanged = true
	}
	if len(upDeps) > 0 {
		deps = append(withoutShadowed(deps, upDeps), upDeps...)
		if len(ecosystems) > 0 {
			ecosystems = append(ecosystems, supplychain.EcosystemUpstream)
		}
	}
	finish := func(result *dependency.UpdateResult, dryRun bool) {
		if lock == nil || dryRun || result == nil {
			return
		}
		for _, a := range result.Applied {
			if a.Dep.Ecosystem == supplychain.EcosystemUpstream {
				if _, healed, ch := lock.Observe(a.Dep.Group, afterBump[a.Dep.Group], now); ch {
					if len(healed) > 0 {
						fmt.Printf("  upstream: %s reset corrupt cooldown timestamp(s) in the lock: %v\n", a.Dep.Group, healed)
					}
					lockChanged = true
				}
			}
		}
		if !lockChanged {
			return
		}
		if err := lock.Write(rootDir); err != nil {
			fmt.Printf("  upstream: could not write %s: %v\n", lockRel(), err)
			return
		}
		result.FilesChanged = append(result.FilesChanged, lockRel())
	}
	return deps, ecosystems, finish
}
