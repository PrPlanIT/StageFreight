package dependency

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// The outdated gate answers a question the update pipeline deliberately does not: how
// far behind is this project, INCLUDING everything policy chose not to apply. A held
// major, a pin above the ceiling, an ecosystem that cannot be auto-written — each is a
// correct decision by the updater and each leaves the project further behind without
// saying so. On an image republished to a moving tag that silence is the hazard: the
// tag keeps making a version claim while the distance from upstream grows.
//
// It is a REPORTING gate by default. An available update is information, and a policy
// that breaks the build the day upstream publishes is one operators switch off, taking
// the signal with it.

// OutdatedItem is one dependency behind its available version, with the magnitude of
// the gap — the operator-facing shape, not the updater's internal decision.
type OutdatedItem struct {
	Name      string
	Ecosystem string
	Current   string
	Latest    string
	Magnitude string // "major" | "minor" | "patch"
}

// OutdatedAtOrAbove returns the dependencies whose available update is at or above the
// given magnitude, newest-gap-first by rank. `at` of "off" (or empty) returns nothing.
//
// The comparison is against Latest — the true newest — NOT UpdateTarget: the point is
// the distance from upstream, and measuring against the target the policy already
// accepted would report zero every time by construction.
func OutdatedAtOrAbove(deps []supplychain.Dependency, at string) []OutdatedItem {
	threshold := magnitudeRank(at)
	if threshold == 0 {
		return nil // "off", empty, or unrecognized — no gate
	}

	var out []OutdatedItem
	for _, d := range deps {
		// An unresolved dependency is not "up to date" — but it is also not a measured
		// gap, and reporting it here would conflate "could not check" with "behind".
		// The unresolved skip category already carries that condition.
		if d.Latest == "" || d.Current == "" || d.Latest == d.Current {
			continue
		}
		delta := version.CompareDependencyVersions(d.Current, d.Latest, d.Ecosystem)
		mag := magnitudeOf(delta)
		if mag == "" || magnitudeRank(mag) < threshold {
			continue
		}
		out = append(out, OutdatedItem{
			Name:      d.Name,
			Ecosystem: d.Ecosystem,
			Current:   d.Current,
			Latest:    d.Latest,
			Magnitude: mag,
		})
	}
	return out
}

// magnitudeRank orders the magnitudes so a threshold can include everything above it.
// Deliberately NOT the updater's updateTypeRank, which folds every unrecognized value
// into patch: here an unrecognized threshold must disable the gate, not silently turn
// it into the most sensitive setting.
func magnitudeRank(m string) int {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "major":
		return 3
	case "minor":
		return 2
	case "patch":
		return 1
	}
	return 0 // "off", empty, or unrecognized
}

// magnitudeOf reduces a version delta to its largest moving component. A delta with no
// positive component (equal, or a comparison the ecosystem could not parse) yields "",
// which the caller treats as "no measurable gap" rather than as a patch.
func magnitudeOf(delta version.VersionDelta) string {
	switch {
	case delta.Major > 0:
		return "major"
	case delta.Minor > 0:
		return "minor"
	case delta.Patch > 0:
		return "patch"
	}
	return ""
}
