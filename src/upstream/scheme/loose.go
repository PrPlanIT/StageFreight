package scheme

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// looseScheme reads the versions semver cannot: 1 to 6 dotted numbers (11.4, 0.6.7.1,
// 2026.7.0), an optional letter directly after the last number marking a later release
// (1.6.60a > 1.6.60), and an optional hyphen suffix marking a prerelease (2.0-rc1 < 2.0).
type looseScheme struct{}

var looseRe = regexp.MustCompile(`(?i)^v?(\d+(?:\.\d+){0,5})([a-z])?(?:-([0-9a-z][0-9a-z.\-]*))?$`)

type looseVersion struct {
	text   string
	parts  []int
	letter string
	pre    string
}

func (looseScheme) Name() string { return Loose }

func (looseScheme) Parse(s string) (Version, error) {
	m := looseRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil, fmt.Errorf("%q is not a dotted version", s)
	}
	v := looseVersion{text: s, letter: strings.ToLower(m[2]), pre: strings.ToLower(m[3])}
	for _, p := range strings.Split(m[1], ".") {
		n, _ := strconv.Atoi(p)
		v.parts = append(v.parts, n)
	}
	return v, nil
}

func (v looseVersion) String() string   { return v.text }
func (v looseVersion) Prerelease() bool { return v.pre != "" }

func (v looseVersion) Compare(o Version) int {
	w := o.(looseVersion)
	if c := cmpParts(v.parts, w.parts); c != 0 {
		return c
	}
	if c := strings.Compare(v.letter, w.letter); c != 0 {
		return c
	}
	switch {
	case v.pre == w.pre:
		return 0
	case v.pre == "":
		return 1
	case w.pre == "":
		return -1
	}
	return comparePre(v.pre, w.pre)
}

var preToken = regexp.MustCompile(`\d+|[a-z]+`)

// comparePre orders prerelease labels piece by piece, numbers numerically (rc2 < rc10).
func comparePre(a, b string) int {
	x, y := preToken.FindAllString(a, -1), preToken.FindAllString(b, -1)
	for i := 0; i < len(x) && i < len(y); i++ {
		xn, xerr := strconv.Atoi(x[i])
		yn, yerr := strconv.Atoi(y[i])
		var c int
		if xerr == nil && yerr == nil {
			c = cmpInt(xn, yn)
		} else {
			c = strings.Compare(x[i], y[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmpInt(len(x), len(y))
}
