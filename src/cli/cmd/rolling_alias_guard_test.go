package cmd

import "testing"

func TestAliasMarkerRoundTrip(t *testing.T) {
	body := aliasMarker("0.39.1-dev+f6637da") + "\nnotes here"
	got, ok := readAliasVersion(body)
	if !ok || got != "0.39.1-dev+f6637da" {
		t.Fatalf("round trip failed: %q %v", got, ok)
	}
	if _, ok := readAliasVersion("no marker at all"); ok {
		t.Fatal("unmarked body must not report a version")
	}
}

// The ordering contract. A rolling alias may only ADVANCE — but the guard must never
// block a deliberate re-cut, and must fail open whenever it cannot prove a regression.
func TestAliasOrdering(t *testing.T) {
	cases := []struct {
		name, incumbent, publishing string
		wantHold                    bool
	}{
		{"older build must be held", "0.39.1-dev", "0.38.0-dev", true},
		{"newer build advances", "0.38.0-dev", "0.39.1-dev", false},
		{"identical version is a re-cut, never blocked", "0.39.1-dev", "0.39.1-dev", false},
		{"patch bump advances", "0.39.0", "0.39.1", false},
		{"prerelease precedes its release", "0.39.1", "0.39.1-dev", true},
		{"unparseable incumbent fails open", "not-a-version", "0.39.1", false},
		{"unparseable candidate fails open", "0.39.1", "not-a-version", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := aliasMarker(c.incumbent) + "\nnotes"
			recorded, ok := readAliasVersion(body)
			if !ok {
				t.Fatal("marker unreadable")
			}
			got := holdsFor(recorded, c.publishing)
			if got != c.wantHold {
				t.Errorf("incumbent=%s publishing=%s hold=%v want %v", c.incumbent, c.publishing, got, c.wantHold)
			}
		})
	}
}
