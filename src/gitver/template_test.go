package gitver

import "testing"

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
