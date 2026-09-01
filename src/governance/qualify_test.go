package governance

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

func TestQualifyLeavesDeclaredSourcesAlone(t *testing.T) {
	q := PresetQualifier{Repo: "https://gitlab.example.com/Org/Policy"}
	cases := []struct{ in, want string }{
		// Unqualified: governance supplies the provenance it lacks.
		{"preset/commit.yml", "https://gitlab.example.com/Org/Policy//preset/commit.yml"},
		{"./preset/commit.yml", "https://gitlab.example.com/Org/Policy//preset/commit.yml"},

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

// THE INVARIANT: qualification supplies a source, never a revision. A distributed
// reference must track the policy repo's default branch, so a satellite follows the
// source instead of freezing at the commit it was reconciled from. Pinning is the
// operator's opt-out, declared per reference — never imposed by governance.
func TestQualifyNeverPins(t *testing.T) {
	q := PresetQualifier{Repo: "https://gitlab.example.com/Org/Policy"}
	got := q.Qualify("preset/lint.yml")
	if got != "https://gitlab.example.com/Org/Policy//preset/lint.yml" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "@") {
		t.Fatalf("qualification pinned the reference: %q", got)
	}
	if k := presetref.Parse(got).Kind; k != presetref.Tracked {
		t.Fatalf("qualified reference is %v, want Tracked", k)
	}
}

func TestQualifyConfigWalksNestedAndLists(t *testing.T) {
	q := PresetQualifier{Repo: "src"}
	cfg := map[string]any{
		"commit":   map[string]any{"preset": "preset/commit.yml"},
		"stencils": map[string]any{"presets": []any{"preset/a.yml", "https://foreign.example/b.yml"}},
	}
	q.QualifyConfig(cfg)
	if got := cfg["commit"].(map[string]any)["preset"]; got != "src//preset/commit.yml" {
		t.Errorf("commit preset = %v", got)
	}
	list := cfg["stencils"].(map[string]any)["presets"].([]any)
	if list[0] != "src//preset/a.yml" {
		t.Errorf("presets[0] = %v", list[0])
	}
	if list[1] != "https://foreign.example/b.yml" {
		t.Errorf("presets[1] was redirected: %v", list[1])
	}
}
