package severity

// maxRank is the rank of the most severe label (Critical) — the ceiling used to
// invert Rank into an ascending sort order.
const maxRank = 4

// Rank maps a severity label to a comparable rank where HIGHER is worse
// (critical=4, high=3, medium=2, low=1, unknown=0). For threshold / "at least
// this severe" comparisons.
func Rank(label string) int {
	switch Normalize(label) {
	case Critical:
		return 4
	case High:
		return 3
	case Medium:
		return 2
	case Low:
		return 1
	default: // Unknown
		return 0
	}
}

// Order returns an ascending sort key where the MOST severe label sorts FIRST
// (critical=0, high=1, medium=2, low=3, unknown=4) — the inverse of Rank, for
// severity-descending displays.
func Order(label string) int {
	return maxRank - Rank(label)
}
