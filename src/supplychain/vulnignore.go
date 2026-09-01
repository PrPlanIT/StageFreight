package supplychain

import (
	"strings"
	"time"
)

// VulnIgnore is an operator's explicit, recorded decision to carry a known advisory:
// which one, why, and until when. It is the only sanctioned way past a blocking
// vulnerability finding — the alternative, excluding a path or a package name, records
// no reason and never lapses, so it silently outlives whatever justified it.
type VulnIgnore struct {
	ID     string // e.g. "GHSA-xxxx-yyyy-zzzz", "CVE-2026-1234", "GO-2026-1234"
	Reason string // why this risk is carried
	Until  string // YYYY-MM-DD; empty = no expiry
}

// ActiveIgnores reduces declared ignores to the advisory IDs suppressed AT now,
// upper-cased for case-insensitive matching.
//
// A malformed or lapsed date does not suppress. That direction is deliberate: an
// exception that outlives its own expiry, or whose expiry never parsed, must fail
// toward the finding being visible again — the risk it was carrying did not expire
// just because the note about it did.
func ActiveIgnores(ignores []VulnIgnore, now time.Time) map[string]bool {
	active := make(map[string]bool, len(ignores))
	for _, ig := range ignores {
		id := strings.ToUpper(strings.TrimSpace(ig.ID))
		if id == "" {
			continue
		}
		if u := strings.TrimSpace(ig.Until); u != "" {
			t, err := time.Parse("2006-01-02", u)
			if err != nil || !now.Before(t) {
				continue
			}
		}
		active[id] = true
	}
	return active
}
