// Package retention implements a restic-style retention engine that works
// with any named+timestamped items (registry tags, forge releases, etc).
// Policies are additive — an item survives if ANY rule wants to keep it.
package retention

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Item is a named, timestamped entity that can be pruned (tag, release, etc).
type Item struct {
	Name      string
	CreatedAt time.Time
}

// Result captures what the retention engine did.
type Result struct {
	Matched int      // items matching the pattern set
	Kept    int      // items kept by policy
	Deleted []string // items successfully deleted
	Skipped []string // items skipped (digest shared with protected item)
	Errors  []error  // errors from individual deletes
}

// Store abstracts listing and deleting items so the same engine
// works for registry tags, forge releases, or any other prunable resource.
type Store interface {
	List(ctx context.Context) ([]Item, error)
	Delete(ctx context.Context, name string) error
}

// IsSkipped checks whether an error from Store.Delete indicates the item
// was intentionally skipped (e.g., digest shared with a protected tag).
// Store implementations return an error satisfying this interface to signal
// a skip rather than a failure.
type skipper interface {
	IsSkipped() bool
}

// Apply lists all items from the store, filters them by patterns (using
// config.MatchPatterns), sorts by creation time descending, applies
// restic-style retention policies, and deletes items not kept.
//
// patterns uses the same syntax as branches/git_tags in the config:
//
//	["^dev-"]              → only items starting with "dev-"
//	["^dev-", "!^dev-keep"]→ dev- items, excluding dev-keep*
//	[]                     → ALL items are candidates
func Apply(ctx context.Context, store Store, patterns []string, policy config.RetentionPolicy) (*Result, error) {
	if !policy.Active() {
		return nil, fmt.Errorf("retention: no active policy (all values zero)")
	}

	result := &Result{}

	items, err := store.List(ctx)
	if err != nil {
		return result, fmt.Errorf("retention: listing items: %w", err)
	}

	// Filter items that match the pattern set
	var allMatched []Item
	for _, item := range items {
		if config.MatchPatterns(patterns, item.Name) {
			allMatched = append(allMatched, item)
		}
	}

	result.Matched = len(allMatched)

	if len(allMatched) == 0 {
		return result, nil
	}

	// Sort by CreatedAt descending (newest first)
	sort.Slice(allMatched, func(i, j int) bool {
		return allMatched[i].CreatedAt.After(allMatched[j].CreatedAt)
	})

	// Separate protected items from retention candidates so they do not
	// consume keep_last or time-bucket slots. Protected items are always
	// kept; retention policies apply only to the remaining candidates.
	protectPatterns := TemplatesToPatterns(policy.Protect)
	isProtected := make([]bool, len(allMatched))
	var candidates []Item
	for i, item := range allMatched {
		if len(protectPatterns) > 0 && config.MatchPatterns(protectPatterns, item.Name) {
			isProtected[i] = true
		} else {
			candidates = append(candidates, item)
		}
	}

	// Apply retention policies only to non-protected candidates.
	keepSet := ApplyPolicies(candidates, policy)

	// Merge back: protected items are always kept, non-protected follow keepSet.
	keepAll := make([]bool, len(allMatched))
	ci := 0
	for i := range allMatched {
		if isProtected[i] {
			keepAll[i] = true
		} else {
			keepAll[i] = keepSet[ci]
			ci++
		}
	}

	// Count kept
	for _, keep := range keepAll {
		if keep {
			result.Kept++
		}
	}

	// Delete items not in the keep set
	for i, item := range allMatched {
		if keepAll[i] {
			continue
		}
		if err := store.Delete(ctx, item.Name); err != nil {
			var skip skipper
			if errors.As(err, &skip) && skip.IsSkipped() {
				result.Skipped = append(result.Skipped, item.Name)
			} else {
				result.Errors = append(result.Errors, fmt.Errorf("deleting %s: %w", item.Name, err))
			}
		} else {
			result.Deleted = append(result.Deleted, item.Name)
		}
	}

	return result, nil
}

// ApplyPolicies evaluates all retention rules and returns a keep/prune decision
// for each candidate. candidates must be sorted newest-first.
// Policies are additive: an item is kept if ANY rule marks it.
func ApplyPolicies(candidates []Item, policy config.RetentionPolicy) []bool {
	keepSet := make([]bool, len(candidates))

	// keep_last: keep the N most recent
	if policy.KeepLast > 0 {
		for i := 0; i < len(candidates) && i < policy.KeepLast; i++ {
			keepSet[i] = true
		}
	}

	// Time-bucket policies: for each bucket, keep the newest item that falls in it.
	if policy.KeepDaily > 0 {
		ApplyTimeBucket(candidates, keepSet, policy.KeepDaily, TruncateToDay)
	}
	if policy.KeepWeekly > 0 {
		ApplyTimeBucket(candidates, keepSet, policy.KeepWeekly, TruncateToWeek)
	}
	if policy.KeepMonthly > 0 {
		ApplyTimeBucket(candidates, keepSet, policy.KeepMonthly, TruncateToMonth)
	}
	if policy.KeepYearly > 0 {
		ApplyTimeBucket(candidates, keepSet, policy.KeepYearly, TruncateToYear)
	}

	return keepSet
}

// BucketFn truncates a time to the start of its bucket period.
type BucketFn func(time.Time) time.Time

// ApplyTimeBucket keeps the newest item in each of the last N distinct time buckets.
// candidates must be sorted newest-first.
func ApplyTimeBucket(candidates []Item, keepSet []bool, count int, bucket BucketFn) {
	seen := make(map[time.Time]bool)

	for i, item := range candidates {
		if item.CreatedAt.IsZero() {
			continue
		}

		key := bucket(item.CreatedAt)
		if seen[key] {
			continue // already have a newer item for this bucket
		}

		seen[key] = true
		keepSet[i] = true

		if len(seen) >= count {
			break
		}
	}
}

// TruncateToDay truncates a time to the start of its day.
func TruncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// TruncateToWeek truncates a time to the start of its ISO week (Monday).
func TruncateToWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	d := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, t.Location())
}

// TruncateToMonth truncates a time to the first day of its month.
func TruncateToMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// TruncateToYear truncates a time to the first day of its year.
func TruncateToYear(t time.Time) time.Time {
	return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
}
