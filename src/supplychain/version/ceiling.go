package version

import "strings"

// CeilingTarget selects the highest version in `available` whose update-type from
// `current` does not exceed the max_update ceiling ("major" > "minor" > "patch";
// empty/unknown defaults to "minor"). It is the re-target used when a dependency's
// natural latest exceeds the ceiling but a lower in-ceiling upgrade exists — e.g.
// patch-lock grabbing the newest patch of the CURRENT minor rather than holding on
// a minor bump. Only versions strictly newer than `current` are considered.
//
// Returns "" when nothing in `available` is both newer than `current` and within
// the ceiling — the caller then holds the dependency. Yanked/prerelease filtering
// is the caller's responsibility (it has that metadata); this is pure selection.
func CeilingTarget(current string, available []string, maxUpdate, ecosystem string) string {
	cur := ParseVersion(strings.TrimSpace(current))
	if cur == nil {
		return ""
	}
	ceiling := ceilingRank(maxUpdate)

	var bestRaw string
	var best = cur
	for _, raw := range available {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		v := ParseVersion(raw)
		if v == nil || !v.GreaterThan(best) {
			continue
		}
		// Update-type from current must be within the ceiling.
		delta := CompareDependencyVersions(current, raw, ecosystem)
		if updateTypeRank(DominantUpdateType(delta)) > ceiling {
			continue
		}
		best, bestRaw = v, raw
	}
	return bestRaw
}

// ceilingRank maps a max_update ceiling to a comparable rank. Empty/unknown =
// "minor" (StageFreight's default ceiling).
func ceilingRank(maxUpdate string) int {
	switch maxUpdate {
	case "patch":
		return 1
	case "major":
		return 3
	default: // minor
		return 2
	}
}

// updateTypeRank maps an update-type ("major"/"minor"/"patch") to the same scale
// as ceilingRank so the two can be compared. Non-version bumps (tag/security) are
// treated as patch-level.
func updateTypeRank(ut string) int {
	switch ut {
	case "major":
		return 3
	case "minor":
		return 2
	default: // patch, tag, security, unknown
		return 1
	}
}
