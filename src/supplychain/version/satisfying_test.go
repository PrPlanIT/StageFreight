package version

import "testing"

// TestLatestSatisfying: the declared operator is honored, not force-careted.
func TestLatestSatisfying(t *testing.T) {
	avail := []string{"1.8.0", "1.8.5", "1.9.0", "2.0.0"}
	cases := []struct{ constraint, want string }{
		{"^1.8.0", "1.9.0"}, // caret: highest within major 1
		{"~1.8.0", "1.8.5"}, // tilde: highest 1.8.x
		{"=1.8.0", "1.8.0"}, // exact pin honored (was force-careted before)
		{"1.8.0", "1.9.0"},  // bare → caret convention
		{"1.8.*", "1.8.5"},  // wildcard patch line
		{"=9.9.9", ""},      // nothing satisfies
		{"", ""},            // empty constraint
	}
	for _, tc := range cases {
		if got := LatestSatisfying(tc.constraint, avail); got != tc.want {
			t.Errorf("LatestSatisfying(%q) = %q, want %q", tc.constraint, got, tc.want)
		}
	}
}

// TestCaretIfBare: bare versions get caret; operator/wildcard/range pass through.
func TestCaretIfBare(t *testing.T) {
	cases := map[string]string{
		"1.8.0": "^1.8.0", "v1.8.0": "^1.8.0",
		"=1.8.0": "=1.8.0", "~1.2": "~1.2", "1.8.*": "1.8.*", ">=1, <2": ">=1, <2",
	}
	for in, want := range cases {
		if got := caretIfBare(in); got != want {
			t.Errorf("caretIfBare(%q) = %q, want %q", in, got, want)
		}
	}
}
