package version

import "testing"

// precision must reflect the numeric components of the ORIGINAL tag token.
func TestCountVersionPrecision(t *testing.T) {
	cases := map[string]int{
		"8":           1,
		"8.3":         2,
		"8.3.1":       3,
		"1.40.2.8395": 4,
		"noble":       0,
	}
	for in, want := range cases {
		if got := countVersionPrecision(in); got != want {
			t.Errorf("countVersionPrecision(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestIsVersionLike(t *testing.T) {
	versionLike := []string{"1.2.3", "8.3", "8", "v0.31.1", "1.18.4", "v2"}
	for _, v := range versionLike {
		if !IsVersionLike(v) {
			t.Errorf("IsVersionLike(%q) = false, want true", v)
		}
	}
	notVersionLike := []string{"develop", "master", "main", "release-1.2", "latest", "", "  "}
	for _, v := range notVersionLike {
		if IsVersionLike(v) {
			t.Errorf("IsVersionLike(%q) = true, want false", v)
		}
	}
}
