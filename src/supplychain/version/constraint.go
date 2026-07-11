package version

import (
	"fmt"
	"strings"
)

// A toolchain constraint is either an exact version ("1.26.4") or a suffix-
// contiguous wildcard ("1.26.x", "1.x.x", "x.x.x"). A wildcard's FIXED (non-x)
// leading components define a version line; selection picks the highest available
// member of that line. This file owns constraint GRAMMAR and MATCHING only —
// selection policy (which member to adopt) is the caller's; verification (sha256)
// is elsewhere.

// splitConstraint normalizes a constraint/version to its dot-separated components,
// tolerating a leading "v".
func splitConstraint(s string) []string {
	return strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
}

// IsWildcardConstraint reports whether c contains a wildcard component ("x"/"X").
func IsWildcardConstraint(c string) bool {
	for _, s := range splitConstraint(c) {
		if s == "x" || s == "X" {
			return true
		}
	}
	return false
}

// ValidateConstraint checks a constraint's grammar and returns the first problem,
// or nil. Rules: each component is a non-negative integer or the wildcard "x";
// wildcards must be trailing (suffix-contiguous — no fixed component after an "x");
// and a bare partial ("1.26": fewer than 3 fixed components, no wildcard) is
// rejected as ambiguous — write "1.26.x" for the line or a full version.
func ValidateConstraint(c string) error {
	segs := splitConstraint(c)
	if len(segs) == 0 || (len(segs) == 1 && segs[0] == "") {
		return fmt.Errorf("empty constraint")
	}
	seenWildcard := false
	for _, s := range segs {
		switch {
		case s == "x" || s == "X":
			seenWildcard = true
		case isAllDigits(s):
			if seenWildcard {
				return fmt.Errorf("constraint %q: fixed component %q follows a wildcard — wildcards must be trailing (e.g. 1.26.x, not 1.x.4)", c, s)
			}
		default:
			return fmt.Errorf("constraint %q: component %q is neither a number nor the wildcard x", c, s)
		}
	}
	if !seenWildcard && len(segs) < 3 {
		return fmt.Errorf("constraint %q: bare partial is ambiguous — write %q for the line, or a full version like %s.0", c, strings.Join(segs, ".")+".x", strings.Join(segs, "."))
	}
	return nil
}

// ConstraintMatches reports whether version v satisfies constraint c: every FIXED
// component of c equals v's corresponding component; wildcard components match
// anything (and, being trailing, end the check). Assumes c passed ValidateConstraint.
func ConstraintMatches(c, v string) bool {
	cs := splitConstraint(c)
	vs := splitConstraint(v)
	for i, seg := range cs {
		if seg == "x" || seg == "X" {
			return true // trailing wildcard — remaining components are free
		}
		if i >= len(vs) || vs[i] != seg {
			return false
		}
	}
	return true
}

// SelectConstraint returns the highest STABLE version in `available` that satisfies
// c, or "" if none. Pre-releases are excluded (deterministic default). This is the
// candidate-set-to-selection step for a resolved constraint.
func SelectConstraint(c string, available []string) string {
	var best string
	var bestV = ParseVersion("0.0.0")
	for _, raw := range available {
		if !ConstraintMatches(c, raw) {
			continue
		}
		v := ParseVersion(raw)
		if v == nil || v.Prerelease() != "" {
			continue
		}
		if best == "" || v.GreaterThan(bestV) {
			bestV, best = v, strings.TrimPrefix(strings.TrimSpace(raw), "v")
		}
	}
	return best
}
