package config

import "strings"

// EventMatches reports whether the current CI event satisfies a target's
// when.events filter. It is the shared primitive for event-aware target routing
// (release targets, binary-archive gating).
//
// Semantics:
//   - An empty filter (no events: declared) means no filtering — always true.
//   - An empty current event (unknown) also means no filtering — events are
//     enforced only when the event is known, so non-CI/manual paths stay lenient.
//   - Comparison is case-insensitive and trims surrounding whitespace.
func EventMatches(events []string, current string) bool {
	if len(events) == 0 || current == "" {
		return true
	}
	for _, e := range events {
		if strings.EqualFold(strings.TrimSpace(e), current) {
			return true
		}
	}
	return false
}
