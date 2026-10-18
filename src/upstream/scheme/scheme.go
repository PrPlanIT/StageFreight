// Package scheme parses and orders version strings by a named versioning scheme. Comparing
// everything as semver gives wrong answers for upstreams that are not semver (PEP 440
// 2.22.0b1, 2-part 11.4, letter-suffixed 1.6.60a), so each source names its scheme and its
// versions are only ever compared with versions of the same scheme.
package scheme

import (
	"fmt"
	"sort"
)

// Version is one parsed version.
type Version interface {
	// String is the version's canonical text.
	String() string
	// Compare orders this version against another of the SAME scheme: -1, 0, +1.
	Compare(other Version) int
	// Prerelease reports whether the scheme calls this version a prerelease.
	Prerelease() bool
}

// Claimer is implemented by versions whose scheme distinguishes packaging from upstream (a
// Debian revision): Claims reports whether our release version ours names this one. Schemes
// without it use Compare == 0.
type Claimer interface {
	Claims(ours Version) bool
}

// Same reports whether our version ours names the upstream release v.
func Same(v, ours Version) bool {
	if c, ok := v.(Claimer); ok {
		return c.Claims(ours)
	}
	return v.Compare(ours) == 0
}

// Scheme parses versions of one versioning scheme.
type Scheme interface {
	Name() string
	// Parse reads a version, an optional leading v already removed by the caller or not.
	Parse(s string) (Version, error)
}

// Scheme names.
const (
	Semver = "semver"
	PEP440 = "pep440"
	Loose  = "loose"
	Debian = "debian"
)

var schemes = map[string]Scheme{
	Semver: semverScheme{},
	PEP440: pep440Scheme{},
	Loose:  looseScheme{},
	Debian: debianScheme{},
}

// Get returns the named scheme.
func Get(name string) (Scheme, error) {
	if s, ok := schemes[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("unknown version scheme %q (%s)", name, Names())
}

// Names lists the scheme names, sorted.
func Names() []string {
	out := make([]string, 0, len(schemes))
	for n := range schemes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpParts compares numeric release segments, padding the shorter with zeros (1.2 == 1.2.0).
func cmpParts(a, b []int) int {
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if c := cmpInt(x, y); c != 0 {
			return c
		}
	}
	return 0
}
