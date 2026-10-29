package scheme

import "testing"

// ordered asserts each version sorts strictly before the next.
func ordered(t *testing.T, name string, versions ...string) {
	t.Helper()
	s, err := Get(name)
	if err != nil {
		t.Fatal(err)
	}
	var prev Version
	for _, text := range versions {
		v, err := s.Parse(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if prev != nil && prev.Compare(v) >= 0 {
			t.Errorf("%s: %s should sort before %s", name, prev, v)
		}
		prev = v
	}
}

func equal(t *testing.T, name, a, b string) {
	t.Helper()
	s, _ := Get(name)
	x, err1 := s.Parse(a)
	y, err2 := s.Parse(b)
	if err1 != nil || err2 != nil || x.Compare(y) != 0 {
		t.Errorf("%s: %s and %s should be equal (%v %v)", name, a, b, err1, err2)
	}
}

func TestSemver(t *testing.T) {
	ordered(t, Semver, "1.2.0-rc.1", "1.2.0", "1.10.0", "v2.0.0")
	equal(t, Semver, "11.4", "11.4.0")
}

func TestPEP440(t *testing.T) {
	// The spec's own ordering example, abridged.
	ordered(t, PEP440, "1.0.dev456", "1.0a1", "1.0a2.dev456", "1.0a12", "1.0b1", "1.0rc1", "1.0", "1.0.post456.dev34", "1.0.post456", "1.1.dev1", "1!0.5")
	ordered(t, PEP440, "v2.21.5", "v2.22.0b1", "v2.22.0rc1", "v2.22.0")
	equal(t, PEP440, "1.0alpha1", "1.0a1")
	equal(t, PEP440, "1.0-c1", "1.0rc1")
	equal(t, PEP440, "1.0", "1.0.0")
	s, _ := Get(PEP440)
	for text, pre := range map[string]bool{"2.22.0b1": true, "1.0.dev0": true, "1.0.post1": false, "2.21.5": false} {
		v, err := s.Parse(text)
		if err != nil || v.Prerelease() != pre {
			t.Errorf("%s: prerelease=%v err=%v, want %v", text, v != nil && v.Prerelease(), err, pre)
		}
	}
	if _, err := s.Parse("devel-tip"); err == nil {
		t.Error("devel-tip is not a version")
	}
}

func TestLoose(t *testing.T) {
	ordered(t, Loose, "1.6.59", "1.6.60-rc2", "1.6.60-rc10", "1.6.60", "1.6.60a", "1.6.60b", "1.6.61")
	ordered(t, Loose, "0.6.7", "0.6.7.1", "0.6.8", "2026.7.0")
	equal(t, Loose, "11.4", "11.4.0")
	s, _ := Get(Loose)
	if _, err := s.Parse("2.0.0rc1"); err == nil {
		t.Error("a multi-letter suffix glued to a number is not a loose version (that is PEP 440)")
	}
}

func TestDebian(t *testing.T) {
	// dpkg's own ordering cases.
	ordered(t, Debian, "1.0~rc1-1", "1.0-1", "1.0-1.1", "1.0-2", "1.0a-1", "1.0+dfsg-1", "1.1-1", "1:0.9-1")
	ordered(t, Debian, "3.7.4-1", "3.7.5-1", "3.7.5-1.1")
	ordered(t, Debian, "2.8.3-r0", "2.8.3-r4", "2.8.3-r10", "2.8.4-r0") // apk revisions
	equal(t, Debian, "1.0-01", "1.0-1")
	s, _ := Get(Debian)
	pre, _ := s.Parse("1.0~beta1-1")
	if !pre.Prerelease() {
		t.Error("~ marks a Debian prerelease")
	}
	rel, _ := s.Parse("3.7.5-1")
	ours, _ := s.Parse("v3.7.5")
	other, _ := s.Parse("3.7.5-2")
	if !Same(rel, ours) || Same(rel, other) {
		t.Error("our revision-less 3.7.5 names Debian's 3.7.5-1; an explicit revision must match exactly")
	}
	if _, err := s.Parse("latest"); err == nil {
		t.Error("latest is not a Debian version")
	}
}

func TestUnknownScheme(t *testing.T) {
	if _, err := Get("calver"); err == nil {
		t.Fatal("unknown scheme accepted")
	}
}
