package discovery

import (
	"time"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// cooldownFor is the min_release_age governing dep: a toolchain's own (normalization
// fills it from toolchains.min_release_age), else the dependency default.
func (m *Resolver) cooldownFor(dep supplychain.Dependency) supplychain.Cooldown {
	if dep.Ecosystem == supplychain.EcosystemToolchain {
		if c, ok := m.desired[dep.Name]; ok && c.MinReleaseAge != nil {
			return *c.MinReleaseAge
		}
	}
	return m.cfg.MinReleaseAge
}

// applyCooldowns enforces min_release_age across every resolved dependency, after
// vulnerability correlation so a vulnerable dependency can bypass it. It is the one
// place the cooldown is decided; freshness and the update pass both read its result.
func (m *Resolver) applyCooldowns(deps []supplychain.Dependency) {
	now := m.clock()
	for i := range deps {
		applyCooldown(&deps[i], m.cooldownFor(deps[i]), now)
	}
}

// applyCooldown steps Latest and LatestEligible back to the newest releases old enough
// for their update size, and drops younger versions from AvailableVersions so a
// max_update re-target cannot pick one. A dependency whose registry gives no dates is
// left alone. An undated version counts as old enough only when it sorts at or below a
// dated release already past every window; otherwise it counts as too young.
//
// Security fixes bypass the cooldown: when only a young release carries a known
// vulnerability's fix, that release is kept and disclosed as CooldownBypassed. An old
// enough release that also fixes it is preferred, and an advisory with no fix bypasses
// nothing.
func applyCooldown(dep *supplychain.Dependency, cd supplychain.Cooldown, now time.Time) {
	if cd.IsZero() || len(dep.Released) == 0 || dep.Pinned != "" {
		return
	}
	size := func(v string) string {
		if dep.LockPending || dep.Current == "" {
			return "" // no installed version to measure from: the longest window
		}
		return version.DominantUpdateType(version.CompareDependencyVersions(dep.Current, v, dep.Ecosystem))
	}
	pastAll := "" // the highest stable release past every window
	for v, t := range dep.Released {
		if now.Sub(t) >= cd.Longest() && !version.IsPrerelease(v) && (pastAll == "" || compare(v, pastAll, dep.Ecosystem) > 0) {
			pastAll = v
		}
	}
	aged := func(v string) bool {
		if t, ok := dep.Released[v]; ok {
			return now.Sub(t) >= cd.Window(size(v))
		}
		return pastAll != "" && compare(v, pastAll, dep.Ecosystem) <= 0
	}
	fix := "" // the version fixing every advisory with a known fix
	for _, v := range dep.Vulnerabilities {
		if v.FixedIn != "" && (fix == "" || compare(v.FixedIn, fix, dep.Ecosystem) > 0) {
			fix = v.FixedIn
		}
	}

	// newestAged is the highest aged release at or below ceiling and above Current,
	// satisfying the dependency's constraint when constrained.
	newestAged := func(ceiling string, constrained bool) string {
		best := ""
		for v := range dep.Released {
			switch {
			case !aged(v),
				version.IsPrerelease(v) && !version.IsPrerelease(dep.Current),
				compare(v, ceiling, dep.Ecosystem) > 0,
				!dep.LockPending && dep.Current != "" && compare(v, dep.Current, dep.Ecosystem) <= 0,
				constrained && dep.Constraint != "" && !version.Satisfies(dep.Constraint, v):
				continue
			}
			if best == "" || compare(v, best, dep.Ecosystem) > 0 {
				best = v
			}
		}
		return best
	}
	// stepBack is what a young target t yields: the newest aged release below it, or t
	// itself when only t (of the two) carries the fix.
	stepBack := func(t string, constrained bool) string {
		v := newestAged(t, constrained)
		if fix != "" && compare(t, fix, dep.Ecosystem) >= 0 && (v == "" || compare(v, fix, dep.Ecosystem) < 0) {
			dep.CooldownBypassed = t
			return t
		}
		return v
	}

	held := ""
	if t := dep.LatestEligible; t != "" && t != dep.Current && !aged(t) {
		if v := stepBack(t, true); v != t {
			held = t
			dep.LatestEligible = firstNonEmpty(v, dep.Current)
		}
	}
	switch t := dep.Latest; {
	case t == "" || aged(t):
	case dep.LockPending:
		// A first lock is born at the newest aged release; a line with none yet keeps
		// its newest, since the tool must run at something.
		if v := stepBack(t, true); v != "" && v != t {
			held, dep.Latest, dep.Current = t, v, v
		}
	case t != dep.Current:
		// Latest is the update target itself when there is no separate compatible one.
		if v := stepBack(t, dep.LatestEligible == ""); v != t {
			held = t
			dep.Latest = firstNonEmpty(v, dep.LatestEligible, dep.Current)
		}
	}
	dep.CooldownHeld = held

	if len(dep.AvailableVersions) > 0 {
		kept := dep.AvailableVersions[:0:0]
		for _, v := range dep.AvailableVersions {
			if dep.Current == "" || compare(v, dep.Current, dep.Ecosystem) <= 0 || aged(v) || v == dep.CooldownBypassed {
				kept = append(kept, v)
			}
		}
		dep.AvailableVersions = kept
	}
}

// compare orders two versions by semver precedence (prereleases below their release),
// falling back to the ecosystem-aware component comparison.
func compare(a, b, ecosystem string) int {
	if va, vb := version.ParseVersion(a), version.ParseVersion(b); va != nil && vb != nil {
		return va.Compare(vb)
	}
	return version.CompareVersions(a, b, ecosystem)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
