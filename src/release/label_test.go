package release

import "testing"

// TestReleaseLabel: a declared release tag heads the notes as written; anything else
// (a dev build, a channel tag) keeps "v" + version.
func TestReleaseLabel(t *testing.T) {
	patterns := []string{`^v?\d+\.\d+\.\d+$`, `^(beta|proto)_v\d+\.\d+$`}
	cases := []struct{ tag, version, want string }{
		{"v0.2.1", "0.2.1", "v0.2.1"},
		{"beta_v0.614", "beta_v0.614", "beta_v0.614"},
		{"dev-1a2b3c4d", "0.2.1-dev+1a2b3c4d", "v0.2.1-dev+1a2b3c4d"},
		{"", "0.2.1-dev+1a2b3c4d", "v0.2.1-dev+1a2b3c4d"},
	}
	for _, tc := range cases {
		if got := releaseLabel(NotesInput{Tag: tc.tag, TagPatterns: patterns}, tc.version); got != tc.want {
			t.Errorf("tag %q version %q → %q, want %q", tc.tag, tc.version, got, tc.want)
		}
	}
}
