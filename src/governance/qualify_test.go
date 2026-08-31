package governance

import "testing"

func TestQualifyLeavesDeclaredSourcesAlone(t *testing.T) {
	q := PresetQualifier{Repo: "https://gitlab.example.com/Org/Policy", Ref: "main"}
	cases := []struct{ in, want string }{
		// Unqualified: governance supplies the provenance it lacks.
		{"preset/commit.yml", "https://gitlab.example.com/Org/Policy//preset/commit.yml@main"},
		{"./preset/commit.yml", "https://gitlab.example.com/Org/Policy//preset/commit.yml@main"},

		// THE INVARIANT: a reference that names its own source is never redirected,
		// whatever governance is configured to be.
		{"https://foreign.example/b.yml", "https://foreign.example/b.yml"},
		{"github:OtherOrg/presets//x.yml", "github:OtherOrg/presets//x.yml"},
		{"github:OtherOrg/presets//x.yml@refs/tags/v2", "github:OtherOrg/presets//x.yml@refs/tags/v2"},
	}
	for _, c := range cases {
		if got := q.Qualify(c.in); got != c.want {
			t.Errorf("Qualify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// No ref means the source's default branch, so nothing is appended.
func TestQualifyUnpinnedTracksDefaultBranch(t *testing.T) {
	q := PresetQualifier{Repo: "https://gitlab.example.com/Org/Policy"}
	if got := q.Qualify("preset/lint.yml"); got != "https://gitlab.example.com/Org/Policy//preset/lint.yml" {
		t.Errorf("got %q", got)
	}
}

func TestQualifyConfigWalksNestedAndLists(t *testing.T) {
	q := PresetQualifier{Repo: "src", Ref: "main"}
	cfg := map[string]any{
		"commit":   map[string]any{"preset": "preset/commit.yml"},
		"stencils": map[string]any{"presets": []any{"preset/a.yml", "https://foreign.example/b.yml"}},
	}
	q.QualifyConfig(cfg)
	if got := cfg["commit"].(map[string]any)["preset"]; got != "src//preset/commit.yml@main" {
		t.Errorf("commit preset = %v", got)
	}
	list := cfg["stencils"].(map[string]any)["presets"].([]any)
	if list[0] != "src//preset/a.yml@main" {
		t.Errorf("presets[0] = %v", list[0])
	}
	if list[1] != "https://foreign.example/b.yml" {
		t.Errorf("presets[1] was redirected: %v", list[1])
	}
}
