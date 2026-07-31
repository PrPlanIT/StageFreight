package scribe

import (
	"testing"

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
		"t": {ID: "t", Type: "text", Content: "hi {var:who}"},    // text with a var
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
