package retention

import (
	"context"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// memStore is an in-memory Store for testing.
type memStore struct {
	items   []Item
	deleted []string
}

func (s *memStore) List(_ context.Context) ([]Item, error) {
	return s.items, nil
}

func (s *memStore) Delete(_ context.Context, name string) error {
	s.deleted = append(s.deleted, name)
	return nil
}

func TestApply_ProtectedTagDoesNotConsumeKeepLast(t *testing.T) {
	now := time.Now()

	// 5 dev tags + 1 protected tag that also matches the candidate pattern.
	// keep_last=3, protect=["latest-dev"].
	// Expected: 3 non-protected dev tags kept + 1 protected = 4 kept, 2 deleted.
	store := &memStore{
		items: []Item{
			{Name: "dev-e", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "latest-dev", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "dev-d", CreatedAt: now.Add(-2 * time.Hour)},
			{Name: "dev-c", CreatedAt: now.Add(-3 * time.Hour)},
			{Name: "dev-b", CreatedAt: now.Add(-4 * time.Hour)},
			{Name: "dev-a", CreatedAt: now.Add(-5 * time.Hour)},
		},
	}

	policy := config.RetentionPolicy{
		KeepLast: 3,
		Protect:  []string{"latest-dev"},
	}

	// All items match (empty patterns = match all)
	result, err := Apply(context.Background(), store, nil, policy)
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if result.Matched != 6 {
		t.Errorf("Matched = %d; want 6", result.Matched)
	}

	// 3 dev tags kept by keep_last + 1 protected = 4 total kept
	if result.Kept != 4 {
		t.Errorf("Kept = %d; want 4", result.Kept)
	}

	// dev-b and dev-a should be deleted (oldest, beyond keep_last)
	if len(result.Deleted) != 2 {
		t.Fatalf("Deleted count = %d; want 2, got %v", len(result.Deleted), result.Deleted)
	}
	wantDeleted := map[string]bool{"dev-a": true, "dev-b": true}
	for _, d := range result.Deleted {
		if !wantDeleted[d] {
			t.Errorf("unexpected deletion: %s", d)
		}
	}

	// latest-dev must NOT appear in deleted
	for _, d := range store.deleted {
		if d == "latest-dev" {
			t.Error("latest-dev was deleted despite being protected")
		}
	}
}

func TestApply_ProtectedTagAloneDoesNotPreventDeletion(t *testing.T) {
	now := time.Now()

	// Edge case: all non-protected tags exceed keep_last.
	// 4 tags, keep_last=1, protect=["keep-me"].
	// Expected: keep-me kept (protected) + 1 kept by policy = 2 kept, 2 deleted.
	store := &memStore{
		items: []Item{
			{Name: "tag-c", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "keep-me", CreatedAt: now.Add(-2 * time.Hour)},
			{Name: "tag-b", CreatedAt: now.Add(-3 * time.Hour)},
			{Name: "tag-a", CreatedAt: now.Add(-4 * time.Hour)},
		},
	}

	policy := config.RetentionPolicy{
		KeepLast: 1,
		Protect:  []string{"keep-me"},
	}

	result, err := Apply(context.Background(), store, nil, policy)
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if result.Kept != 2 {
		t.Errorf("Kept = %d; want 2", result.Kept)
	}

	if len(result.Deleted) != 2 {
		t.Fatalf("Deleted count = %d; want 2, got %v", len(result.Deleted), result.Deleted)
	}

	wantDeleted := map[string]bool{"tag-b": true, "tag-a": true}
	for _, d := range result.Deleted {
		if !wantDeleted[d] {
			t.Errorf("unexpected deletion: %s", d)
		}
	}
}
