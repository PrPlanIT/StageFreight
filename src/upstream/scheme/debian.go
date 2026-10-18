package scheme

import (
	"fmt"
	"strconv"
	"strings"
)

// debianScheme is dpkg's version scheme: [epoch:]upstream[-revision], compared the way
// dpkg --compare-versions does (letters before non-letters, ~ before everything, even the
// end). Alpine's apk versions (1.2.3-r4) order correctly under it too.
type debianScheme struct{}

type debianVersion struct {
	text     string
	epoch    int
	upstream string
	revision string
}

func (debianScheme) Name() string { return Debian }

func (debianScheme) Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	v := debianVersion{text: s}
	rest := s
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		e, err := strconv.Atoi(rest[:i])
		if err != nil {
			return nil, fmt.Errorf("%q: bad epoch", s)
		}
		v.epoch, rest = e, rest[i+1:]
	}
	if i := strings.LastIndexByte(rest, '-'); i >= 0 {
		v.upstream, v.revision = rest[:i], rest[i+1:]
	} else {
		v.upstream = rest
	}
	v.upstream = strings.TrimPrefix(v.upstream, "v")
	if v.upstream == "" || v.upstream[0] < '0' || v.upstream[0] > '9' {
		return nil, fmt.Errorf("%q is not a Debian version (the upstream part must start with a digit)", s)
	}
	return v, nil
}

func (v debianVersion) String() string   { return v.text }
func (v debianVersion) Prerelease() bool { return strings.Contains(v.upstream, "~") }

func (v debianVersion) Compare(o Version) int {
	w := o.(debianVersion)
	if c := cmpInt(v.epoch, w.epoch); c != 0 {
		return c
	}
	if c := verrevcmp(v.upstream, w.upstream); c != 0 {
		return c
	}
	return verrevcmp(v.revision, w.revision)
}

// Claims: a version of ours with no packaging revision claims the upstream version, so it
// matches any packaging revision of it (our v3.7.5 is Debian's 3.7.5-1).
func (v debianVersion) Claims(ours Version) bool {
	w := ours.(debianVersion)
	if w.revision != "" {
		return v.Compare(w) == 0
	}
	return v.epoch == w.epoch && verrevcmp(v.upstream, w.upstream) == 0
}

// order is dpkg's character weight: ~ sorts first, then the end of the string (and digits),
// then letters, then everything else.
func order(c byte) int {
	switch {
	case c == '~':
		return -1
	case c >= '0' && c <= '9':
		return 0
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return int(c)
	case c == 0:
		return 0
	}
	return int(c) + 256
}

// verrevcmp is dpkg's comparison of an upstream version or a revision.
func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		for (i < len(a) && (a[i] < '0' || a[i] > '9')) || (j < len(b) && (b[j] < '0' || b[j] > '9')) {
			var ac, bc int
			if i < len(a) && (a[i] < '0' || a[i] > '9') {
				ac = order(a[i])
			} else if i >= len(a) {
				ac = order(0)
			}
			if j < len(b) && (b[j] < '0' || b[j] > '9') {
				bc = order(b[j])
			} else if j >= len(b) {
				bc = order(0)
			}
			if ac != bc {
				return cmpInt(ac, bc)
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		diff := 0
		for i < len(a) && a[i] >= '0' && a[i] <= '9' && j < len(b) && b[j] >= '0' && b[j] <= '9' {
			if diff == 0 {
				diff = cmpInt(int(a[i]), int(b[j]))
			}
			i++
			j++
		}
		if i < len(a) && a[i] >= '0' && a[i] <= '9' {
			return 1
		}
		if j < len(b) && b[j] >= '0' && b[j] <= '9' {
			return -1
		}
		if diff != 0 {
			return diff
		}
	}
	return 0
}
