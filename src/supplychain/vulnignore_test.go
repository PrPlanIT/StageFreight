package supplychain

import (
	"testing"
	"time"
)

// An exception is a decision with an expiry, so the cases that matter are the ones where
// it must STOP applying: a date that has passed, and a date that never parsed. Both have
// to fail toward the advisory being visible again — the risk did not expire just because
// the note about it did.
func TestActiveIgnores(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		ig   VulnIgnore
		want bool
	}{
		{"no expiry suppresses", VulnIgnore{ID: "GHSA-aaaa"}, true},
		{"future expiry suppresses", VulnIgnore{ID: "GHSA-aaaa", Until: "2026-12-31"}, true},
		{"lapsed expiry does not", VulnIgnore{ID: "GHSA-aaaa", Until: "2026-08-31"}, false},
		{"expiry today does not", VulnIgnore{ID: "GHSA-aaaa", Until: "2026-09-01"}, false},
		{"malformed expiry does not", VulnIgnore{ID: "GHSA-aaaa", Until: "soon"}, false},
		{"empty id is not an exception", VulnIgnore{Until: "2026-12-31"}, false},
	} {
		got := ActiveIgnores([]VulnIgnore{c.ig}, now)["GHSA-AAAA"]
		if got != c.want {
			t.Errorf("%s: active=%v, want %v", c.name, got, c.want)
		}
	}

	// Matching is case-insensitive: advisories are quoted in mixed case across sources.
	if !ActiveIgnores([]VulnIgnore{{ID: "  ghsa-BbBb  "}}, now)["GHSA-BBBB"] {
		t.Error("id matching must ignore case and surrounding space")
	}
}
