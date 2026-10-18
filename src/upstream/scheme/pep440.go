package scheme

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// pep440Scheme is Python's PEP 440: [N!]N(.N)*[{a|b|rc}N][.postN][.devN][+local], with the
// spec's normalization of spellings (alpha → a, c/pre/preview → rc, - and _ separators).
type pep440Scheme struct{}

// pep440Re is the PEP 440 appendix's permissive pattern.
var pep440Re = regexp.MustCompile(`(?i)^v?` +
	`(?:(\d+)!)?` + // epoch
	`(\d+(?:\.\d+)*)` + // release
	`(?:[-_.]?(alpha|a|beta|b|preview|pre|c|rc)[-_.]?(\d+)?)?` + // pre
	`(?:-(\d+)|[-_.]?(post|rev|r)[-_.]?(\d+)?)?` + // post
	`(?:[-_.]?(dev)[-_.]?(\d+)?)?` + // dev
	`(?:\+([a-z0-9]+(?:[-_.][a-z0-9]+)*))?$`) // local

type pep440Version struct {
	text    string
	epoch   int
	release []int
	pre     *[2]int // phase (0 a, 1 b, 2 rc), number
	post    *int
	dev     *int
}

func (pep440Scheme) Name() string { return PEP440 }

func (pep440Scheme) Parse(s string) (Version, error) {
	m := pep440Re.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil, fmt.Errorf("%q is not a PEP 440 version", s)
	}
	num := func(t string) int {
		n, _ := strconv.Atoi(t)
		return n
	}
	v := pep440Version{text: s, epoch: num(m[1])}
	for _, p := range strings.Split(m[2], ".") {
		v.release = append(v.release, num(p))
	}
	if m[3] != "" {
		phase := 2
		switch strings.ToLower(m[3]) {
		case "a", "alpha":
			phase = 0
		case "b", "beta":
			phase = 1
		}
		v.pre = &[2]int{phase, num(m[4])}
	}
	switch {
	case m[5] != "":
		p := num(m[5])
		v.post = &p
	case m[6] != "":
		p := num(m[7])
		v.post = &p
	}
	if m[8] != "" {
		d := num(m[9])
		v.dev = &d
	}
	return v, nil
}

func (v pep440Version) String() string   { return v.text }
func (v pep440Version) Prerelease() bool { return v.pre != nil || v.dev != nil }

// key orders per PEP 440: a dev-only release sorts before its prereleases, a release without
// a prerelease after them; no post-release sorts first; no dev-release sorts last.
func (v pep440Version) key() (pre [2]int, post, dev int) {
	switch {
	case v.pre != nil:
		pre = *v.pre
	case v.post == nil && v.dev != nil:
		pre = [2]int{-1, 0}
	default:
		pre = [2]int{math.MaxInt, 0}
	}
	post = -1
	if v.post != nil {
		post = *v.post
	}
	dev = math.MaxInt
	if v.dev != nil {
		dev = *v.dev
	}
	return
}

func (v pep440Version) Compare(o Version) int {
	w := o.(pep440Version)
	if c := cmpInt(v.epoch, w.epoch); c != 0 {
		return c
	}
	if c := cmpParts(v.release, w.release); c != 0 {
		return c
	}
	vp, vpost, vdev := v.key()
	wp, wpost, wdev := w.key()
	for _, c := range []int{cmpInt(vp[0], wp[0]), cmpInt(vp[1], wp[1]), cmpInt(vpost, wpost), cmpInt(vdev, wdev)} {
		if c != 0 {
			return c
		}
	}
	return 0
}
