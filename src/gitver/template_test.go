package gitver

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/version"
)

// TestResolveTemplate_StageFreightVersion pins that {stagefreight.version} resolves to
// StageFreight's OWN tool version (version.Version) and is not clobbered by the repo's
// {version} — the two are distinct facts sharing the leaf-pass.
func TestResolveTemplate_StageFreightVersion(t *testing.T) {
	v := &VersionInfo{Version: "0.6.1"}
	got := ResolveTemplateWithDirAndVars("sf {stagefreight.version} repo {version}", v, "", nil)
	want := "sf " + version.Version + " repo 0.6.1"
	if got != want {
		t.Errorf("ResolveTemplateWithDirAndVars = %q, want %q", got, want)
	}
}

// TestResolveTags_ChannelPatterns guards the channel naming layer: the immutable
// dev-tag pattern `dev-{sha:8}` resolves to a fixed 8-char short SHA, a rolling
// alias (`latest-dev`) passes through unchanged, and bare `{sha}` keeps its 7-char
// default. Release channels depend on all three, so this pins the resolution so a
// future template change can't silently break channel tag minting.
func TestResolveTags_ChannelPatterns(t *testing.T) {
	v := &VersionInfo{SHA: "0420ec8abcdef0123456", Base: "0.6.1", Version: "0.6.1"}
	got := ResolveTags([]string{"dev-{sha:8}", "latest-dev", "dev-{sha}"}, v)
	want := []string{"dev-0420ec8a", "latest-dev", "dev-0420ec8"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ResolveTags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
