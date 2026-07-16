package reconcile

import (
	"fmt"

	masterminds "github.com/Masterminds/semver/v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// goToolchainName identifies the reconciler in Mutation/ConfigError records.
const goToolchainName = "go-toolchain"

// GoBuilderObservation is one golang builder image FROM line paired with the `go`
// directive floor of the module that governs it and the registry's published
// golang tags. It is the PURE input to go-toolchain reconciliation: all IO —
// reading go.mod, reading the Dockerfile FROM line, listing registry tags — is
// performed by the adapter that assembles these. The reconciler only derives.
type GoBuilderObservation struct {
	File          string   // repo-relative Dockerfile path
	Line          int      // 1-based FROM line
	Image         string   // image reference minus tag, e.g. "golang" or "docker.io/library/golang"
	CurrentTag    string   // the builder's current tag on disk, e.g. "1.24.13", "1.24-alpine3.18"
	Floor         string   // the governing module's `go` directive, e.g. "1.25.0"
	AvailableTags []string // published tags for the builder image (a pure registry fact)
	FloorSource   string   // human label of the floor's origin, e.g. "go.mod: go 1.25.0"
}

// ReconcileGoToolchain derives, for each observation, whether the golang builder
// satisfies its module's `go` directive floor and — when it does not — the
// canonical tag that does, preserving the operator's variant and tag granularity.
// It never selects among valid futures: the floor's minor line and the operator's
// variant fully determine the target, or the configuration is unreconcilable.
func ReconcileGoToolchain(obs []GoBuilderObservation) Result {
	var res Result
	for _, o := range obs {
		mut, cerr := reconcileGoBuilder(o)
		switch {
		case cerr != nil:
			res.ConfigErrors = append(res.ConfigErrors, *cerr)
		case mut != nil:
			res.Mutations = append(res.Mutations, *mut)
		}
	}
	return res
}

// reconcileGoBuilder is the pure per-observation decision.
//   - (nil, nil):        the builder already satisfies the floor, or the comparison
//     is not determinable — never a false positive.
//   - (mutation, nil):   the builder violates the floor and a canonical satisfying
//     tag was derived.
//   - (nil, configError): the builder violates the floor and NO canonical satisfying
//     tag is representable — a human must resolve it.
func reconcileGoBuilder(o GoBuilderObservation) (*Mutation, *ConfigError) {
	cur := version.DecomposeTag(o.CurrentTag)
	if cur.Version == nil {
		// Non-versioned builder ("bookworm", "latest") floats to the newest image;
		// there is no representable pin to compare against the floor.
		return nil, nil
	}
	floor, err := masterminds.NewVersion(o.Floor)
	if err != nil {
		// Unparseable directive — nothing authoritative to enforce.
		return nil, nil
	}

	if goBuilderSatisfies(cur, floor) {
		return nil, nil
	}

	target, ok := canonicalGoTag(cur, floor, o.AvailableTags)
	if !ok {
		return nil, &ConfigError{
			Reconciler: goToolchainName,
			File:       o.File,
			Line:       o.Line,
			Message: fmt.Sprintf(
				"%s requires Go >= %s but builder %s:%s does not satisfy it, and no %s:%d.%d%s tag is published to reconcile to — resolve manually",
				o.FloorSource, o.Floor, o.Image, o.CurrentTag,
				o.Image, floor.Major(), floor.Minor(), variantLabel(cur.Suffix),
			),
		}
	}

	return &Mutation{
		Reconciler: goToolchainName,
		File:       o.File,
		Line:       o.Line,
		From:       o.Image + ":" + o.CurrentTag,
		To:         o.Image + ":" + target,
		Authority:  o.FloorSource,
	}, nil
}

// goBuilderSatisfies reports whether a builder tag satisfies the floor, honoring
// the tag's granularity. A minor-pinned tag ("1.24") floats to the newest patch of
// that minor and a major-pinned tag ("1") to the newest of that major, so their
// satisfaction is judged at that granularity; only a full patch pin ("1.24.13") is
// compared exactly. This is what prevents falsely reconciling a "golang:1.25" that
// already satisfies "go 1.25.4".
func goBuilderSatisfies(cur version.DecomposedTag, floor *masterminds.Version) bool {
	cv := cur.Version
	if cv.Major() != floor.Major() {
		return cv.Major() > floor.Major()
	}
	// Same major.
	switch {
	case cur.Precision <= 1:
		return true // "golang:1" floats to the newest minor of the major
	case cur.Precision == 2:
		return cv.Minor() >= floor.Minor() // "golang:1.24" floats to the newest patch of the minor
	default:
		return !cv.LessThan(floor) // exact patch pin
	}
}

// canonicalGoTag derives the minimal published golang tag that satisfies the floor
// while preserving the operator's variant suffix and tag granularity. The target
// minor line is the floor's OWN minor (never overshoot); within it the newest
// satisfying stable patch is chosen the same way StageFreight materializes any
// pinned line. Returns ("", false) when no such tag is published — the caller
// reports a ConfigError rather than substituting a different variant, changing the
// operator's granularity, or jumping to a higher minor.
func canonicalGoTag(cur version.DecomposedTag, floor *masterminds.Version, tags []string) (string, bool) {
	// Candidate tags on the floor's exact minor line, same variant, stable, and at
	// or above the floor — proof that the operator's chosen line exists at all.
	var cands []version.DecomposedTag
	for _, t := range tags {
		dt := version.DecomposeTag(t)
		if dt.Version == nil || version.IsDateLikeVersion(dt.Version) {
			continue
		}
		if dt.Suffix != cur.Suffix { // preserve the variant exactly (alpine stays alpine)
			continue
		}
		if dt.PreRank != 0 { // stable only
			continue
		}
		if dt.Version.Major() != floor.Major() || dt.Version.Minor() != floor.Minor() {
			continue // stay on the floor's minor — never overshoot
		}
		if dt.Version.LessThan(floor) {
			continue // must satisfy the floor
		}
		cands = append(cands, dt)
	}
	best := version.LatestInFamily(cands)
	if best == nil {
		return "", false
	}

	// Render at the operator's granularity. A patch pin ("1.24.13") becomes the
	// newest satisfying patch. A minor pin ("1.24") or major pin ("1") is preserved
	// as such ONLY if that exact floating tag is published; otherwise the operator's
	// chosen granularity is not representable at the floor and we fail rather than
	// silently tighten it to a patch.
	switch {
	case cur.Precision >= 3:
		return best.Raw, true
	case cur.Precision == 2:
		return renderFloatingTag(fmt.Sprintf("%d.%d", floor.Major(), floor.Minor()), cur.Suffix, tags)
	default: // precision <= 1: major pin
		return renderFloatingTag(fmt.Sprintf("%d", floor.Major()), cur.Suffix, tags)
	}
}

// renderFloatingTag builds a floating tag string ("1.25" or "1.25-alpine3.18") and
// returns it only if it is actually published, preserving the operator's chosen
// granularity. Returns ("", false) when the floating tag does not exist.
func renderFloatingTag(versionPart, suffix string, tags []string) (string, bool) {
	want := versionPart
	if suffix != "" {
		want = want + "-" + suffix
	}
	for _, t := range tags {
		if t == want {
			return want, true
		}
	}
	return "", false
}

// variantLabel renders a suffix for human messages ("-alpine3.18", or "" when bare).
func variantLabel(suffix string) string {
	if suffix == "" {
		return ""
	}
	return "-" + suffix
}
