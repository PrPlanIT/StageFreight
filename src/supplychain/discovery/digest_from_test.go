package discovery

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestSplitImageDigest(t *testing.T) {
	cases := []struct {
		ref, wantRef, wantDigest string
	}{
		{"alpine:3.23.5@sha256:abc", "alpine:3.23.5", "sha256:abc"},
		{"docker.io/library/alpine:3.23.5@sha256:abc", "docker.io/library/alpine:3.23.5", "sha256:abc"},
		{"alpine:3.23.5", "alpine:3.23.5", ""},
		{"alpine", "alpine", ""},
	}
	for _, c := range cases {
		gotRef, gotDigest := SplitImageDigest(c.ref)
		if gotRef != c.wantRef || gotDigest != c.wantDigest {
			t.Errorf("SplitImageDigest(%q) = (%q, %q), want (%q, %q)",
				c.ref, gotRef, gotDigest, c.wantRef, c.wantDigest)
		}
	}
}

// A digest-pinned Alpine base must still resolve its version for base/apk detection —
// SplitImageTag alone drops the tag on an "@"-containing ref, so the digest is stripped first.
func TestDetectAlpineVersion_DigestPinned(t *testing.T) {
	info := &supplychain.DockerFreshnessInfo{
		Stages: []supplychain.StageInfo{
			{Image: "docker.io/library/alpine:3.23.5@sha256:abc"},
		},
	}
	if got := detectAlpineVersion(info); got != "3.23.5" {
		t.Errorf("detectAlpineVersion = %q, want %q", got, "3.23.5")
	}
}
