package gitver

import (
	"regexp"
	"testing"
	"time"
)

// Only base_from consumes tag order, so only a base_from source that actually picks an
// older tag than one it matches is flagged; identity-only and date-sortable sources are quiet.
func TestNonSemverOrderingWarnings(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2024, 9, d, 0, 0, 0, 0, time.UTC) }
	dates := map[string]time.Time{
		"beta_v0.614": day(18), "beta_v0.9": day(1), "proto_v0.600": day(7),
		"2024.09.18": day(18), "2024.09.07": day(7),
		"v1.2.0": day(1), "v1.1.5": day(20),
	}
	committed := func(tag string) (time.Time, bool) { at, ok := dates[tag]; return at, ok }
	sources := map[string]*regexp.Regexp{
		"stable":   regexp.MustCompile(`^v\d+\.\d+\.\d+$`),
		"original": regexp.MustCompile(`^(beta|proto)_v\d+\.\d+$`),
		"dated":    regexp.MustCompile(`^\d{4}\.\d{2}\.\d{2}$`),
	}
	// Lexical-descending order, as ListTagsSorted yields non-semver tags after semver.
	tags := []string{"v1.2.0", "v1.1.5", "proto_v0.600", "beta_v0.9", "beta_v0.614", "2024.09.18", "2024.09.07"}
	rule := func(ids ...string) []BranchRule { return []BranchRule{{ID: "default", IsDefault: true, BaseFromIDs: ids}} }

	if w := nonSemverOrderingWarnings(tags, sources, rule("stable"), committed); len(w) != 0 {
		t.Errorf("original is not on a base_from path and stable orders semantically; want no warning, got %v", w)
	}
	if w := nonSemverOrderingWarnings(tags, sources, rule("dated"), committed); len(w) != 0 {
		t.Errorf("date-named tags sort by date; want no warning, got %v", w)
	}
	w := nonSemverOrderingWarnings(tags, sources, rule("original"), committed)
	if len(w) != 1 || !regexp.MustCompile(`"original" takes "proto_v0.600", but "beta_v0.614" is newer`).MatchString(w[0]) {
		t.Errorf("base_from original picks proto_v0.600 over the newer beta_v0.614; want one precise warning, got %v", w)
	}
}
