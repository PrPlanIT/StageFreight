package discovery

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

func repeatVer(v string, n int) []string {
	s := make([]string, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func TestCollectMatchingReleaseTags(t *testing.T) {
	t.Run("line on page 1 → stops early", func(t *testing.T) {
		pages := [][]string{
			append(repeatVer("1.30.0", 99), "1.26.7"), // full page incl. match
			append(repeatVer("1.29.0", 99), "1.26.5"), // must NOT be fetched
		}
		fetched := 0
		tags := collectMatchingReleaseTags("1.26.x", func(p int) ([]string, error) {
			if p > len(pages) {
				return nil, nil
			}
			fetched++
			return pages[p-1], nil
		})
		if got := version.SelectConstraint("1.26.x", tags); got != "1.26.7" {
			t.Errorf("resolved %q, want 1.26.7", got)
		}
		if fetched != 1 {
			t.Errorf("fetched %d pages, want 1 (stop on match)", fetched)
		}
	})

	t.Run("old line → paginates to it", func(t *testing.T) {
		pages := [][]string{
			repeatVer("1.30.0", 100),                  // full page, no match → continue
			append(repeatVer("1.29.0", 99), "1.26.7"), // match on page 2
		}
		fetched := 0
		tags := collectMatchingReleaseTags("1.26.x", func(p int) ([]string, error) {
			if p > len(pages) {
				return nil, nil
			}
			fetched++
			return pages[p-1], nil
		})
		if got := version.SelectConstraint("1.26.x", tags); got != "1.26.7" {
			t.Errorf("resolved %q, want 1.26.7", got)
		}
		if fetched != 2 {
			t.Errorf("fetched %d pages, want 2", fetched)
		}
	})

	t.Run("nonexistent line → bounded by page cap", func(t *testing.T) {
		fetched := 0
		tags := collectMatchingReleaseTags("9.9.x", func(p int) ([]string, error) {
			fetched++
			return repeatVer("1.30.0", 100), nil // always full, never matches
		})
		if got := version.SelectConstraint("9.9.x", tags); got != "" {
			t.Errorf("resolved %q, want empty (no line)", got)
		}
		if fetched != maxReleasePages {
			t.Errorf("fetched %d pages, want cap %d", fetched, maxReleasePages)
		}
	})

	t.Run("partial page → stops (last page)", func(t *testing.T) {
		fetched := 0
		collectMatchingReleaseTags("9.9.x", func(p int) ([]string, error) {
			fetched++
			return repeatVer("1.30.0", 40), nil // < per-page → last
		})
		if fetched != 1 {
			t.Errorf("fetched %d, want 1 (partial page is last)", fetched)
		}
	})
}
