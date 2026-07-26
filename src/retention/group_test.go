package retention

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// fakeStore records deletions for assertions.
type fakeStore struct {
	items   []Item
	deleted map[string]bool
}

func newFakeStore(items ...Item) *fakeStore {
	return &fakeStore{items: items, deleted: map[string]bool{}}
}
func (s *fakeStore) List(context.Context) ([]Item, error) { return s.items, nil }
func (s *fakeStore) Delete(_ context.Context, name string) error {
	s.deleted[name] = true
	return nil
}
func (s *fakeStore) deletedList() []string {
	var d []string
	for n := range s.deleted {
		d = append(d, n)
	}
	sort.Strings(d)
	return d
}

func at(h int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
}

func mustApply(t *testing.T, s *fakeStore, templates []string, pol config.RetentionPolicy) {
	t.Helper()
	if _, err := Apply(context.Background(), s, templates, pol); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func wantDeleted(t *testing.T, s *fakeStore, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := s.deletedList()
	if len(got) != len(want) {
		t.Fatalf("deleted = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deleted = %v, want %v", got, want)
		}
	}
}

// Two accumulating {sha} series in one target must NOT share keep_last slots.
// (Under the old lumped engine, nightly's newer tags evicted the whole dev series.)
func TestApply_MultiSeriesIndependent(t *testing.T) {
	s := newFakeStore(
		Item{"dev-00000001", at(1)}, Item{"dev-00000002", at(2)}, Item{"dev-00000003", at(3)},
		Item{"dev-00000004", at(4)}, Item{"dev-00000005", at(5)},
		Item{"nightly-00000001", at(6)}, Item{"nightly-00000002", at(7)}, Item{"nightly-00000003", at(8)},
		Item{"nightly-00000004", at(9)}, Item{"nightly-00000005", at(10)},
	)
	mustApply(t, s, []string{"dev-{sha:8}", "nightly-{sha:8}"}, config.RetentionPolicy{KeepLast: 4})
	// keep 4 newest per series → prune only the oldest of each.
	wantDeleted(t, s, "dev-00000001", "nightly-00000001")
}

// Per-branch series must not evict each other; a busy branch never prunes a quiet one.
func TestApply_PerBranchNoInterference(t *testing.T) {
	s := newFakeStore(
		Item{"test-gamma-c0000001", at(1)}, Item{"test-gamma-c0000002", at(2)}, Item{"latest-test-gamma", at(2)},
		Item{"test-alpha-a0000001", at(3)}, Item{"test-alpha-a0000002", at(4)}, Item{"test-alpha-a0000003", at(5)}, Item{"latest-test-alpha", at(5)},
		Item{"test-beta-b0000001", at(6)}, Item{"test-beta-b0000002", at(7)}, Item{"test-beta-b0000003", at(8)},
		Item{"test-beta-b0000004", at(9)}, Item{"test-beta-b0000005", at(10)}, Item{"latest-test-beta", at(10)},
	)
	mustApply(t, s, []string{"test-{branch}-{sha:8}", "latest-test-{branch}"}, config.RetentionPolicy{KeepLast: 4})
	// alpha (3≤4) & gamma (2≤4) fully kept; beta trimmed to newest 4; rolling aliases kept.
	wantDeleted(t, s, "test-beta-b0000001")
}

// A rolling template (no sequence var) is never pruned by keep_last.
func TestApply_RollingExempt(t *testing.T) {
	s := newFakeStore(
		Item{"dev-00000001", at(1)}, Item{"dev-00000002", at(2)}, Item{"dev-00000003", at(3)},
		Item{"latest-dev", at(3)},
	)
	mustApply(t, s, []string{"dev-{sha:8}", "latest-dev"}, config.RetentionPolicy{KeepLast: 1})
	if s.deleted["latest-dev"] {
		t.Fatal("latest-dev (rolling) must never be pruned")
	}
	wantDeleted(t, s, "dev-00000001", "dev-00000002")
}

// keep_branches bounds the NUMBER of identity groups per template (retired branches).
func TestApply_KeepBranches(t *testing.T) {
	s := newFakeStore(
		Item{"test-old-00000001", at(1)}, Item{"latest-test-old", at(1)},
		Item{"test-mid-00000002", at(2)}, Item{"latest-test-mid", at(2)},
		Item{"test-new-00000003", at(3)}, Item{"latest-test-new", at(3)},
	)
	// keep_last 0 = ∞ within each branch; keep only the 2 most-recent branches.
	mustApply(t, s, []string{"test-{branch}-{sha:8}", "latest-test-{branch}"}, config.RetentionPolicy{KeepBranches: 2})
	wantDeleted(t, s, "latest-test-old", "test-old-00000001")
}

// 0 / -1 / unset on every rule ⇒ keep everything (graceful no-op, not an error).
func TestApply_InfinityNoOp(t *testing.T) {
	for _, kl := range []int{0, -1} {
		s := newFakeStore(
			Item{"dev-00000001", at(1)}, Item{"dev-00000002", at(2)}, Item{"dev-00000003", at(3)},
		)
		mustApply(t, s, []string{"dev-{sha:8}"}, config.RetentionPolicy{KeepLast: kl})
		if len(s.deleted) != 0 {
			t.Fatalf("keep_last=%d must prune nothing, deleted %v", kl, s.deletedList())
		}
	}
}

// protect is an unconditional keep that overrides keep_last.
func TestApply_ProtectOverrides(t *testing.T) {
	s := newFakeStore(
		Item{"dev-00000001", at(1)}, Item{"dev-00000002", at(2)}, Item{"dev-00000003", at(3)},
	)
	mustApply(t, s, []string{"dev-{sha:8}"}, config.RetentionPolicy{KeepLast: 1, Protect: []string{"dev-00000001"}})
	// keep_last:1 keeps dev-3; protect keeps dev-1; only dev-2 falls out.
	wantDeleted(t, s, "dev-00000002")
}
