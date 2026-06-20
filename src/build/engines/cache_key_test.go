package engines

import (
	"strings"
	"testing"
)

func TestCargoProjectKey_StableAndDistinct(t *testing.T) {
	a1 := cargoProjectKey("/builds/riff.cc/jetpack")
	a2 := cargoProjectKey("/builds/riff.cc/jetpack")
	b := cargoProjectKey("/builds/riff.cc/dragonfly")
	if a1 != a2 {
		t.Errorf("same project must be a stable key: %q != %q", a1, a2)
	}
	if a1 == b {
		t.Error("different projects must get distinct keys (no target-lock contention)")
	}
	if !strings.HasPrefix(a1, "jetpack-") {
		t.Errorf("key should be readable (basename prefix), got %q", a1)
	}
}
