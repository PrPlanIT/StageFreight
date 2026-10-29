package upstream

import (
	"testing"
	"time"
)

// TestLockObserveHealsCorruptTimestamp covers the path pins_test.go does not: a corrupt
// first_seen value used to be silently reset to now every run, so the cooldown never elapsed
// and the version never bumped. Observe now heals it once, reports it, and stays stable.
func TestLockObserveHealsCorruptTimestamp(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l := &Lock{LockfileVersion: LockfileVersion}
	// Plant a corrupt timestamp — what a hand-edit or half-written lock looks like.
	l.Sources = []LockSource{{ID: "src", FirstSeen: map[string]string{"1.0.0": "not-a-timestamp"}}}

	seen, healed, changed := l.Observe("src", []string{"1.0.0"}, now)
	if len(healed) != 1 || healed[0] != "1.0.0" {
		t.Fatalf("a corrupt timestamp should be reported as healed, got %v", healed)
	}
	if !changed {
		t.Fatal("healing should mark the lock changed so it is rewritten")
	}
	if !seen["1.0.0"].Equal(now) {
		t.Fatalf("healed sighting should be reset to now, got %v", seen["1.0.0"])
	}
	// The stored value is now valid, so the next run is stable — no perpetual reset.
	if got := l.source("src").FirstSeen["1.0.0"]; got != now.UTC().Format(time.RFC3339) {
		t.Fatalf("stored timestamp should be rewritten to now, got %q", got)
	}
	later := now.Add(72 * time.Hour)
	if _, healed2, _ := l.Observe("src", []string{"1.0.0"}, later); len(healed2) != 0 {
		t.Fatalf("a healed timestamp should parse cleanly next run, got %v", healed2)
	}
}
