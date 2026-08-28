package version

import "testing"

// A distro tag must decompose into the parts that order it, and anything that is not a
// release codename must decline cheaply — most tags are not codenames.
func TestDecomposeCodename(t *testing.T) {
	cases := []struct {
		tag              string
		ok               bool
		codename, distro string
		ordinal          int
		variant          string
	}{
		{"trixie-slim", true, "trixie", "debian", 13, "slim"},
		{"bookworm", true, "bookworm", "debian", 12, ""},
		{"noble-20240801", true, "noble", "ubuntu", 2404, "20240801"},
		{"3.20-alpine", false, "", "", 0, ""},
		{"latest", false, "", "", 0, ""},
		// Rolling aliases name a moving target, not a release — never orderable.
		{"sid", false, "", "", 0, ""},
		{"testing-slim", false, "", "", 0, ""},
		{"stable", false, "", "", 0, ""},
	}
	for _, c := range cases {
		got, ok := DecomposeCodename(c.tag)
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.tag, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Codename != c.codename || got.Distro != c.distro || got.Ordinal != c.ordinal || got.Variant != c.variant {
			t.Errorf("%s: got %+v", c.tag, got)
		}
	}
}

// Ordering holds within a distro and variant, and must NOT cross either: a variant
// change is a different image, and the two distros' ordinals share no scale.
func TestCompareCodenames(t *testing.T) {
	cases := []struct {
		current, latest string
		newer           bool
		why             string
	}{
		{"trixie-slim", "forky-slim", true, "next debian release, same variant"},
		{"bookworm", "trixie", true, "next debian release, bare tags"},
		{"jammy", "noble", true, "next ubuntu release"},
		{"forky-slim", "trixie-slim", false, "older release is not an upgrade"},
		{"trixie-slim", "trixie-slim", false, "same release"},
		{"trixie-slim", "forky", false, "variant change is a different image"},
		{"trixie", "noble", false, "debian and ubuntu ordinals are not comparable"},
		{"trixie-slim", "sid", false, "rolling alias is not a release"},
	}
	for _, c := range cases {
		if got, _ := CompareCodenames(c.current, c.latest); got != c.newer {
			t.Errorf("%s -> %s: got newer=%v, want %v (%s)", c.current, c.latest, got, c.newer, c.why)
		}
	}
}

// A release jump must register as MAJOR so the outdated gate and the held-for-review
// path both see it: crossing an OS release replaces the package set beneath the image.
func TestDockerImageCodenameComparesAsMajor(t *testing.T) {
	delta := CompareDependencyVersions("trixie-slim", "forky-slim", "docker-image")
	if delta.Major <= 0 {
		t.Errorf("a release jump must be a major delta, got %+v", delta)
	}
	if d := CompareDependencyVersions("trixie-slim", "trixie-slim", "docker-image"); !d.IsZero() {
		t.Errorf("same release must be a zero delta, got %+v", d)
	}
}
