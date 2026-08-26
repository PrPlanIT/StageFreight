package presetref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		raw    string
		kind   Kind
		source string
		path   string
		ref    string
	}{
		// Local: no source separator.
		{"preset/lint.yml", Local, "", "preset/lint.yml", ""},
		{"./preset/lint.yml", Local, "", "./preset/lint.yml", ""},

		// Sourced, no ref → tracked (default branch).
		{"gitlab:Org/Repo//preset/lint.yml", Tracked, "gitlab:Org/Repo", "preset/lint.yml", ""},
		{"https://x/r//p/l.yml", Tracked, "https://x/r", "p/l.yml", ""},

		// Bare ref name → Named (deferred to fetch — branch vs tag unknowable statically).
		{"gitlab:Org/Repo//preset/lint.yml@main", Named, "gitlab:Org/Repo", "preset/lint.yml", "main"},
		{"gitlab:Org/Repo//preset/lint.yml@v1.0", Named, "gitlab:Org/Repo", "preset/lint.yml", "v1.0"},

		// Fully-qualified → deterministic.
		{"src//p.yml@refs/heads/main", Tracked, "src", "p.yml", "refs/heads/main"},
		{"src//p.yml@refs/tags/v1.0", Pinned, "src", "p.yml", "refs/tags/v1.0"},

		// Sha → pinned (7–40 hex).
		{"src//p.yml@1a2b3c4", Pinned, "src", "p.yml", "1a2b3c4"},
		{"src//p.yml@0123456789abcdef0123456789abcdef01234567", Pinned, "src", "p.yml", "0123456789abcdef0123456789abcdef01234567"},

		// A short/non-hex name is NOT a sha → Named.
		{"src//p.yml@dev", Named, "src", "p.yml", "dev"},        // too short for sha, not hex-only anyway
		{"src//p.yml@feature", Named, "src", "p.yml", "feature"}, // 'feature' has non-hex letters
	}
	for _, c := range cases {
		got := Parse(c.raw)
		if got.Kind != c.kind || got.Source != c.source || got.Path != c.path || got.Ref != c.ref {
			t.Errorf("Parse(%q) = {kind:%s source:%q path:%q ref:%q}, want {kind:%s source:%q path:%q ref:%q}",
				c.raw, got.Kind, got.Source, got.Path, got.Ref, c.kind, c.source, c.path, c.ref)
		}
	}
}

// TestIsHexSHA guards the sha boundary: 7-char minimum (git's abbreviated default),
// 40-char maximum, hex-only.
func TestIsHexSHA(t *testing.T) {
	yes := []string{"1a2b3c4", "abcdef0", "0123456789abcdef0123456789abcdef01234567", "ABCDEF1"}
	no := []string{"1a2b3c", "main", "v1.0", "feature", "0123456789abcdef0123456789abcdef012345678" /* 41 */, "ghijklm"}
	for _, s := range yes {
		if !isHexSHA(s) {
			t.Errorf("isHexSHA(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isHexSHA(s) {
			t.Errorf("isHexSHA(%q) = true, want false", s)
		}
	}
}
