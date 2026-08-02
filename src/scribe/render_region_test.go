package scribe

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestRenderRegion_ComposeParity is the byte-identity acceptance gate: renderRegion
// must reproduce the retired render.Compose/ComposeInline bytes EXACTLY — space within a
// row, a blank line between rows on "br", empty elements skipped, no trailing newline —
// and the new body: form must resolve {id} embeds plus the gitver leaf-pass. This guards
// R1 (trailing-newline churn) and R4 (element output preserved verbatim).
func TestRenderRegion_ComposeParity(t *testing.T) {
	appCfg := &config.Config{Vars: map[string]string{"who": "world"}}
	content := map[string]config.StencilDef{
		"a": {ID: "a", Output: ".sf/a.svg", Link: "https://x/a"}, // badge
		"b": {ID: "b", Output: ".sf/b.svg", Link: "https://x/b"}, // badge
		"t": {ID: "t", Type: "text", Body: "hi {var:who}"},       // text with a var
	}
	rawBase := "https://raw/repo/main"
	linkBase := "https://blob/repo/main"
	vi := &gitver.VersionInfo{}

	a := "[![a](https://raw/repo/main/.sf/a.svg)](https://x/a)"
	b := "[![b](https://raw/repo/main/.sf/b.svg)](https://x/b)"

	cases := []struct {
		name string
		file config.FileDef
		want string
	}{
		{"inline row (ComposeInline)", config.FileDef{ID: "r", Inline: true, Items: []string{"a", "b"}}, a + " " + b},
		{"multi-row br (Compose)", config.FileDef{ID: "r", Items: []string{"a", "br", "b"}}, a + "\n\n" + b},
		{"single item, no trailing newline", config.FileDef{ID: "r", Items: []string{"a"}}, a},
		{"text stencil resolves its var", config.FileDef{ID: "r", Items: []string{"t"}}, "hi world"},
		{"leading/double br collapses like Compose", config.FileDef{ID: "r", Items: []string{"br", "a", "br", "br", "b"}}, a + "\n\n" + b},
		{"body: freeform embeds", config.FileDef{ID: "r", Body: "row: {a} and {b}"}, "row: " + a + " and " + b},
		{"body: gitver leaf-pass resolves bare {base}", config.FileDef{ID: "r", Body: "{t} v{base}"}, "hi world v"},
	}
	for _, tc := range cases {
		got, err := renderRegion(appCfg, tc.file, content, linkBase, rawBase, vi, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestTextStencil_Facts covers the fact layer of a text body: recorded cistate
// facts resolve FIRST (before stencil embeds), dotted facts for unrecorded domains
// elide, and the whole render is driven by the state file in rootDir.
func TestTextStencil_Facts(t *testing.T) {
	rootDir := t.TempDir()
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "test", Attempted: true, Completed: true, Outcome: "success",
			Results: map[string]string{"passed": "142", "total": "142", "coverage": "78.4%"},
		})
	}); err != nil {
		t.Fatal(err)
	}

	appCfg := &config.Config{}
	appCfg.Stencils = config.OrderedStencils{
		{ID: "receipts", Type: "text", Body: "Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage\nSecurity — {security.blocking} blocking"},
	}
	got, err := resolveStencilMarkdown(appCfg, appCfg.StencilsByID()["receipts"], "", "", &gitver.VersionInfo{}, rootDir)
	if err != nil {
		t.Fatal(err)
	}
	// The Security line's only token resolved empty (domain unrecorded) → LINE
	// ELISION drops the whole line, authored label included; the blank-run tidy
	// trims the leftover newline.
	want := "Tests — 142/142 passed · 78.4% coverage"
	if got != want {
		t.Errorf("facts render:\n got %q\nwant %q", got, want)
	}
}

// TestTextStencil_Embeds covers the type: text grow-up: a text body resolves {id}
// stencil embeds (recursively, through the same dispatcher) BEFORE the gitver
// leaf-pass, unknown tokens stay literal, and a cycle degrades to the literal token
// at render time (validation rejects declared cycles; this is the backstop).
func TestTextStencil_Embeds(t *testing.T) {
	appCfg := &config.Config{Vars: map[string]string{"who": "world"}}
	appCfg.Stencils = config.OrderedStencils{
		{ID: "inner", Type: "text", Body: "hi {var:who}"},
		{ID: "outer", Type: "text", Body: "say: {inner} · {unknown}"},
		{ID: "loop-a", Type: "text", Body: "a sees {loop-b}"},
		{ID: "loop-b", Type: "text", Body: "b sees {loop-a}"},
	}
	vi := &gitver.VersionInfo{}
	byID := appCfg.StencilsByID()

	got, err := resolveStencilMarkdown(appCfg, byID["outer"], "", "", vi, "")
	if err != nil {
		t.Fatalf("outer: %v", err)
	}
	if want := "say: hi world · {unknown}"; got != want {
		t.Errorf("outer:\n got %q\nwant %q", got, want)
	}

	// The cycle backstop: {loop-a} inside loop-b (already on the stack) stays literal.
	got, err = resolveStencilMarkdown(appCfg, byID["loop-a"], "", "", vi, "")
	if err != nil {
		t.Fatalf("loop-a: %v", err)
	}
	if want := "a sees b sees {loop-a}"; got != want {
		t.Errorf("loop-a:\n got %q\nwant %q", got, want)
	}
}
