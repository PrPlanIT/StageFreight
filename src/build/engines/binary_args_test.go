package engines

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestResolveTemplateVars_FullLeafPass proves the build-args consolidation: args now
// resolve through the shared gitver leaf pass, so families the former narrow resolver
// lacked ({sha:N}, {stagefreight.version}, …) work — while {date} stays an RFC3339 build
// timestamp (not gitver's display YYYY-MM-DD).
func TestResolveTemplateVars_FullLeafPass(t *testing.T) {
	cfg := build.BuildConfig{Version: &build.VersionInfo{Version: "1.2.3", SHA: "abcdef1234567"}}

	got := resolveTemplateVars("v{version} sha={sha:8} sf={stagefreight.version}", cfg)
	if !strings.Contains(got, "v1.2.3") {
		t.Errorf("{version} unresolved in %q", got)
	}
	if !strings.Contains(got, "sha=abcdef12") {
		t.Errorf("{sha:8} unresolved (leaf-pass family) in %q", got)
	}
	if strings.Contains(got, "{stagefreight.version}") {
		t.Errorf("{stagefreight.version} left literal (leaf-pass family) in %q", got)
	}

	// {date} is resolved build-locally to an RFC3339 timestamp, before the leaf pass.
	d := resolveTemplateVars("{date}", build.BuildConfig{Version: &build.VersionInfo{}})
	if !strings.Contains(d, "T") || !strings.HasSuffix(d, "Z") {
		t.Errorf("{date} = %q, want an RFC3339 timestamp", d)
	}
}
