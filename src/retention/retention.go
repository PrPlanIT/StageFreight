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

// Apply lists all items from the store, then prunes them PER SERIES rather than as
// one pool. Each tag is placed into a group keyed by (template, identity-values):
// its template plus the values of that template's identity vars ({branch}, {env}, …).
// The restic-style policy (keep_last + time buckets) is applied INDEPENDENTLY within
// each group, so a `keep_last: 6` keeps 6 of every series — one branch's tags never
// evict another's, and two accumulating templates never share slots.
//
// Group taxonomy:
//   - Accumulating (template has a sequence var like {sha}/{version}): keep_last /
//     buckets prune along the sequence within the group.
//   - Rolling (no sequence var, e.g. "latest-dev"): a single value overwritten in
//     place — nothing to prune, so the whole group is kept.
//   - keep_branches: N bounds the NUMBER of identity groups per template — the N
//     most-recently-active are kept, older ones pruned wholesale (bounds retired
//     branches). Groups with no identity var are unaffected (there is only one).
//
// policy.Protect (and any this-run tags a caller injects into it) is an explicit,
// unconditional keep that overrides all of the above. A policy that keeps everything
// (0 / -1 / unset on every rule) is a graceful no-op.
//
// templates uses the same syntax as branches/git_tags in the config; a "!"-prefixed
// template excludes from candidacy but never forms a group.
func Apply(ctx context.Context, store Store, templates []string, policy config.RetentionPolicy) (*Result, error) {
	result := &Result{}

	// 0 / -1 / unset on every rule ⇒ keep everything: a clean no-op, not an error.
	if !policy.Active() {
		return result, nil
	}

	items, err := store.List(ctx)
	if err != nil {
		return result, fmt.Errorf("retention: listing items: %w", err)
	}

	// Compile matchers for grouping; keep wildcard candidacy (with !/OR semantics)
	// identical to before so what is IN SCOPE does not change — only how in-scope
	// items are partitioned and pruned.
	identity := effectiveIdentity(policy.Identity)
	matchers := make([]tmplMatcher, 0, len(templates))
	for _, t := range templates {
		m, cerr := compileTemplate(t, identity)
		if cerr != nil {
			return result, cerr
		}
		matchers = append(matchers, m)
	}
	patterns := TemplatesToPatterns(templates)
	protectPatterns := TemplatesToPatterns(policy.Protect)

	type group struct {
		matcherIdx int // -1 = catch-all (in scope but matched no positive template)
		items      []Item
	}
	groups := map[string]*group{}
	tmplGroupKeys := map[int][]string{} // matcherIdx (identity-bearing) → its group keys
	keep := map[string]bool{}           // item name → survives
	var inScope []Item

	for _, item := range items {
		if !config.MatchPatterns(patterns, item.Name) {
			continue // out of retention scope entirely
		}
		inScope = append(inScope, item)
		result.Matched++

		// Explicit protect (incl. caller-injected this-run tags): unconditional keep.
		if len(protectPatterns) > 0 && config.MatchPatterns(protectPatterns, item.Name) {
			keep[item.Name] = true
			continue
		}

		// Assign to the first positive template's (identity) group.
		assigned := false
		for mi := range matchers {
			if matchers[mi].negate {
				continue
			}
			key, ok := matchers[mi].groupKey(item.Name)
			if !ok {
				continue
			}
			g := groups[key]
			if g == nil {
				g = &group{matcherIdx: mi}
				groups[key] = g
				if len(matchers[mi].idGroups) > 0 {
					tmplGroupKeys[mi] = append(tmplGroupKeys[mi], key)
				}
			}
			g.items = append(g.items, item)
			assigned = true
			break
		}
		if !assigned {
			g := groups[""]
			if g == nil {
				g = &group{matcherIdx: -1}
				groups[""] = g
			}
			g.items = append(g.items, item)
		}
	}

	if result.Matched == 0 {
		return result, nil
	}

	// keep_branches: for each identity-bearing template, keep only the N most-recent
	// identity groups (ranked by newest tag); drop the rest entirely.
	dropped := map[string]bool{}
	if policy.KeepBranches > 0 {
		for _, keys := range tmplGroupKeys {
			if len(keys) <= policy.KeepBranches {
				continue
			}
			sort.Slice(keys, func(i, j int) bool {
				return newestOf(groups[keys[i]].items).After(newestOf(groups[keys[j]].items))
			})
			for _, k := range keys[policy.KeepBranches:] {
				dropped[k] = true
			}
		}
	}

	// Whether any WITHIN-group (sequence) rule is active. keep_branches alone bounds
	// group count but keeps everything ∞ within each surviving group.
	seqActive := policy.KeepLast > 0 || policy.KeepDaily > 0 ||
		policy.KeepWeekly > 0 || policy.KeepMonthly > 0 || policy.KeepYearly > 0

	// Prune the sequence within each surviving group.
	for key, g := range groups {
		if dropped[key] {
			continue // whole group pruned by keep_branches
		}
		rolling := g.matcherIdx >= 0 && !matchers[g.matcherIdx].hasSeq
		if rolling || !seqActive {
			for _, it := range g.items { // rolling, or no within-group rule ⇒ keep all (∞)
				keep[it.Name] = true
			}
			continue
		}
		sorted := append([]Item{}, g.items...)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		})
		keepSet := ApplyPolicies(sorted, policy)
		for i, it := range sorted {
			if keepSet[i] {
				keep[it.Name] = true
			}
		}
	}

	// Delete in-scope items not kept, newest-first for stable output.
	sort.Slice(inScope, func(i, j int) bool {
		return inScope[i].CreatedAt.After(inScope[j].CreatedAt)
	})
	for _, item := range inScope {
		if keep[item.Name] {
			result.Kept++
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
