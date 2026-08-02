package scribe

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/stencil"
)

// A rendered body must carry NO trailing newline — registry.ReplaceBetween supplies
// its own boundary newlines, so a trailing one would churn the file (R1).
func TestRenderText_NoTrailingNewline(t *testing.T) {
	env := stencil.MapEnv(map[string]string{"a": "X", "b": "Y"})
	got := renderText("{a} {b}", env, &gitver.VersionInfo{}, "", nil)
	if got != "X Y" {
		t.Fatalf("got %q want %q", got, "X Y")
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("body must have no trailing newline: %q", got)
	}
}

// Tokens stencil leaves literal ({base}) are resolved by the gitver leaf-pass.
func TestRenderText_LeafPassResolvesGitverTokens(t *testing.T) {
	env := stencil.MapEnv(map[string]string{"a": "A"})
	got := renderText("{a} v{base}", env, &gitver.VersionInfo{Base: "1.2.3"}, "", nil)
	if got != "A v1.2.3" {
		t.Fatalf("leaf-pass should resolve {base}: got %q", got)
	}
}
