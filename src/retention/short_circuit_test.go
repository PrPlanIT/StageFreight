package retention

import (
	"context"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// abortErr implements the aborter interface, the way a *registry.HTTPError with a
// 401/403 status does — signalling a store-wide (credential) failure.
type abortErr struct{}

func (abortErr) Error() string         { return "insufficient scope" }
func (abortErr) AbortsRetention() bool { return true }

// abortingStore fails every Delete with a store-wide error and counts attempts.
type abortingStore struct {
	items    []Item
	attempts int
}

func (s *abortingStore) List(_ context.Context) ([]Item, error) { return s.items, nil }

func (s *abortingStore) Delete(_ context.Context, _ string) error {
	s.attempts++
	return abortErr{}
}

// A store-wide failure (e.g. a token missing delete scope) must abort after the
// first delete: the remaining candidates are recorded as Blocked, not retried.
func TestApply_ShortCircuitsOnStoreWideFailure(t *testing.T) {
	now := time.Now()
	store := &abortingStore{
		items: []Item{
			{Name: "dev-e", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "dev-d", CreatedAt: now.Add(-2 * time.Hour)},
			{Name: "dev-c", CreatedAt: now.Add(-3 * time.Hour)},
			{Name: "dev-b", CreatedAt: now.Add(-4 * time.Hour)},
			{Name: "dev-a", CreatedAt: now.Add(-5 * time.Hour)},
		},
	}

	// keep_last 1 → 1 kept, 4 candidates to prune. The first delete aborts.
	result, err := Apply(context.Background(), store, nil, config.RetentionPolicy{KeepLast: 1})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if store.attempts != 1 {
		t.Errorf("Delete attempts = %d; want 1 (short-circuit after the first store-wide failure)", store.attempts)
	}
	if len(result.Errors) != 1 {
		t.Errorf("Errors = %d; want 1 (only the triggering delete)", len(result.Errors))
	}
	if len(result.Blocked) != 3 {
		t.Errorf("Blocked = %d; want 3 (remaining candidates recorded, not attempted)", len(result.Blocked))
	}
	if len(result.Deleted) != 0 {
		t.Errorf("Deleted = %d; want 0", len(result.Deleted))
	}
}
