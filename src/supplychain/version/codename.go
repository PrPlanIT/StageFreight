package version

import "strings"

// Distro base images are tagged by release CODENAME (debian:trixie-slim,
// ubuntu:noble) rather than by a version, so semver comparison cannot order them and
// every such image resolves as "unresolved (could not verify latest version)" — the
// same report an unreachable registry produces. That conflates "there is no ordering"
// with "the check failed", and it hides the single most consequential update a
// container has: the OS it is built on.
//
// Codenames are an ordered series, so they can be ordered — just not by parsing. The
// ordinal below is the release number (Debian) or YYMM (Ubuntu); only the ORDER
// matters, never the magnitude. Distros are kept apart because their ordinals share no
// scale: comparing a Debian 13 against an Ubuntu 2404 is meaningless, and an image
// repository only ever carries one distro's codenames anyway.
//
// Rolling aliases (sid, testing, unstable, stable, experimental, rc-buggy, devel) are
// deliberately ABSENT. They name a moving target rather than a release, so ordering
// against them would produce an upgrade suggestion that changes meaning underneath the
// operator.

type distroRelease struct {
	distro  string // "debian" | "ubuntu" — ordinals are not comparable across these
	ordinal int
	// stable marks a RELEASED suite. A codename that exists but has not shipped
	// (Debian testing/unstable, an Ubuntu devel series) is recognized so it can still
	// be parsed and ordered, but is never offered as an upgrade TARGET: proposing a
	// move onto testing is proposing an unreleased OS, and under the freshness module
	// a major update is CRITICAL, so it does not merely suggest — it blocks the
	// pipeline on an upgrade nobody should take yet.
	stable bool
}

var distroCodenames = map[string]distroRelease{
	// Debian — ordinal is the release number. trixie is the current stable; forky is
	// testing and duke is the one after, so neither is an upgrade target yet. Promote
	// them here when Debian releases them.
	"jessie":   {"debian", 8, true},
	"stretch":  {"debian", 9, true},
	"buster":   {"debian", 10, true},
	"bullseye": {"debian", 11, true},
	"bookworm": {"debian", 12, true},
	"trixie":   {"debian", 13, true},
	"forky":    {"debian", 14, false}, // testing
	"duke":     {"debian", 15, false}, // not yet released

	// Ubuntu — ordinal is YYMM, which orders LTS and interim releases together.
	"trusty":   {"ubuntu", 1404, true},
	"xenial":   {"ubuntu", 1604, true},
	"bionic":   {"ubuntu", 1804, true},
	"focal":    {"ubuntu", 2004, true},
	"impish":   {"ubuntu", 2110, true},
	"jammy":    {"ubuntu", 2204, true},
	"kinetic":  {"ubuntu", 2210, true},
	"lunar":    {"ubuntu", 2304, true},
	"mantic":   {"ubuntu", 2310, true},
	"noble":    {"ubuntu", 2404, true},
	"oracular": {"ubuntu", 2410, true},
	"plucky":   {"ubuntu", 2504, true},
	"questing": {"ubuntu", 2510, true},
}

// DecomposedCodename is a distro tag split into the parts that order it: the release
// itself and the image variant, which must match for two tags to be comparable
// (trixie-slim upgrades to forky-slim, never to a full forky).
type DecomposedCodename struct {
	Codename string // "trixie"
	Distro   string // "debian"
	Ordinal  int    // 13
	Variant  string // "slim", or "" for a bare codename tag
	Stable   bool   // false for a release that has not shipped (testing/devel)
}

// DecomposeCodename splits a distro base-image tag, reporting false when the leading
// segment is not a known release codename — which is the common case and must stay
// cheap and silent.
func DecomposeCodename(tag string) (DecomposedCodename, bool) {
	name, variant := tag, ""
	if i := strings.IndexByte(tag, '-'); i > 0 {
		name, variant = tag[:i], tag[i+1:]
	}
	rel, ok := distroCodenames[strings.ToLower(name)]
	if !ok {
		return DecomposedCodename{}, false
	}
	return DecomposedCodename{
		Codename: name, Distro: rel.distro, Ordinal: rel.ordinal,
		Variant: variant, Stable: rel.stable,
	}, true
}

// CompareCodenames reports whether latest is a newer release of the same distro and
// image variant than current. A variant change is not an upgrade: it is a different
// image, and treating it as one would silently swap a slim base for a full one.
func CompareCodenames(current, latest string) (newer bool, ordinalDelta int) {
	c, okc := DecomposeCodename(current)
	l, okl := DecomposeCodename(latest)
	if !okc || !okl || c.Distro != l.Distro || c.Variant != l.Variant {
		return false, 0
	}
	return l.Ordinal > c.Ordinal, l.Ordinal - c.Ordinal
}
