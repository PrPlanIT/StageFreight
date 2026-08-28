package k8s

import (
	"testing"
	"time"
)

func missingFor(d time.Duration, now time.Time) *InventoryManifest {
	since := now.Add(-d)
	return &InventoryManifest{
		Apps: map[string]AppManifest{
			"ns/app": {Lifecycle: AppLifecycle{State: "missing", MissingSince: &since}},
		},
	}
}

// A workload absent from one sweep must NOT be retired. Restarts, drains, image pulls
// and failovers all finish well inside the window, and burying them there is the churn
// this guards against.
func TestGraveyardWaitsForSustainedAbsence(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	m := missingFor(GraveyardAfter-time.Minute, now)
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "missing" {
		t.Errorf("just inside the window: got %q, want missing", got)
	}
}

// Sustained absence still retires — the buffer delays the decision, it does not remove it.
func TestGraveyardAfterThreshold(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	m := missingFor(GraveyardAfter+time.Minute, now)
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "graveyard" {
		t.Errorf("past the window: got %q, want graveyard", got)
	}
}

// A missing entry with no MissingSince (written before this field was populated) must
// not be trapped in missing forever.
func TestGraveyardWithoutMissingSince(t *testing.T) {
	now := time.Now().UTC()
	m := &InventoryManifest{Apps: map[string]AppManifest{
		"ns/app": {Lifecycle: AppLifecycle{State: "missing"}},
	}}
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "graveyard" {
		t.Errorf("no MissingSince: got %q, want graveyard", got)
	}
}

// Revival stays immediate and clears the clock: if it is back, it is back.
func TestSightingRevivesAndResetsClock(t *testing.T) {
	now := time.Now().UTC()
	m := missingFor(GraveyardAfter/2, now)
	ReconcileLifecycle(m, []AppRecord{{Key: AppKey{Namespace: "ns", Identity: "app"}}}, true, now)
	got := m.Apps["ns/app"].Lifecycle
	if got.State != "active" {
		t.Errorf("got %q, want active", got.State)
	}
	if got.MissingSince != nil {
		t.Error("MissingSince must be cleared on revival, or the next absence inherits a stale clock")
	}
}
