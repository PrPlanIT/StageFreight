package toolchain

import "testing"

// isGoMajorMinor must accept only bare major.minor (the undownloadable directive
// form) and reject already-precise full versions — we must never re-resolve those.
func TestIsGoMajorMinor(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1.24", true},
		{"1.22", true},
		{"1.24.0", false}, // full patch — leave it alone
		{"1.26.1", false},
		{"1", false},
		{"", false},
		{"1.x", false},
		{"1.24.", false},
		{"go1.24", false},
	}
	for _, c := range cases {
		if got := isGoMajorMinor(c.in); got != c.want {
			t.Errorf("isGoMajorMinor(%q)=%v want %v", c.in, got, c.want)
		}
	}
}
