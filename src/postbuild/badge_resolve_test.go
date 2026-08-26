package postbuild

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestResolveBadgeValues_LeafPassAndLiterals verifies the shared badge-resolution
// pipeline runs the gitver leaf pass per value and passes literals/empties through.
// With no {registry.*}/{inventory.*} tokens present, the batch passes are offline
// no-ops — so this exercises the shared path without network. The two badge
// generators (postbuild hook + CLI) both route through this function; keeping their
// resolution identical is the point of extracting it.
func TestResolveBadgeValues_LeafPassAndLiterals(t *testing.T) {
	specs := []config.BadgeSpec{
		{Value: "v{version}"},
		{Value: "plain-text"},
		{Value: ""},
		{Value: "{stagefreight.version}"},
	}
	vi := &gitver.VersionInfo{Version: "1.2.3"}
	cfg := &config.Config{}

	got := ResolveBadgeValues(context.Background(), specs, vi, "", cfg)

	if len(got) != len(specs) {
		t.Fatalf("got %d values, want %d", len(got), len(specs))
	}
	if got[0] != "v1.2.3" {
		t.Errorf("value[0] = %q, want %q", got[0], "v1.2.3")
	}
	if got[1] != "plain-text" {
		t.Errorf("value[1] = %q, want %q (literal passthrough)", got[1], "plain-text")
	}
	if got[2] != "" {
		t.Errorf("value[2] = %q, want empty (empty passthrough)", got[2])
	}
	// {stagefreight.version} resolves to the tool's own ldflags version — non-empty
	// and distinct from the repo {version}; assert it no longer carries a raw token.
	if got[3] == "" || got[3] == "{stagefreight.version}" {
		t.Errorf("value[3] = %q, want resolved stagefreight version", got[3])
	}
}

// TestResolveBadgeValues_NilVersion verifies a nil VersionInfo skips the leaf pass
// (leaving tokens intact) rather than panicking — matching the per-value guard both
// callers previously inlined.
func TestResolveBadgeValues_NilVersion(t *testing.T) {
	specs := []config.BadgeSpec{{Value: "v{version}"}}
	got := ResolveBadgeValues(context.Background(), specs, nil, "", &config.Config{})
	if got[0] != "v{version}" {
		t.Errorf("value[0] = %q, want %q (leaf pass skipped when VersionInfo is nil)", got[0], "v{version}")
	}
}
