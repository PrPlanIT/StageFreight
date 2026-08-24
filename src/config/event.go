package config

import "strings"

// pushEquivalent reports whether a canonical event distributes like a push — i.e.
// satisfies an events:[push] gate. Both a manual re-run ("manual" — the canonical
// form of web/api/trigger/workflow_dispatch/Manual) and a scheduled run are mainline
// builds that must rebuild and publish the same rolling artifacts a push would: the
// "Run pipeline" rebuild button, and automated periodic releases (dependency bumps,
// fork rebases) that run on a schedule with no human push. The relation is one-way —
// a plain push does NOT satisfy a manual-only or schedule-only gate.
func pushEquivalent(canonicalEvent string) bool {
	return canonicalEvent == "manual" || canonicalEvent == "schedule"
}

// EventMatches reports whether the current CI event satisfies a target's
// when.events filter. It is the shared primitive for event-aware target routing
// (release targets, binary-archive gating). The current event is expected in the
// canonical vocabulary (see NormalizeEvent) — CIEvent normalizes at the env
// boundary, so this primitive never sees a raw forge source name.
//
// Semantics:
//   - An empty filter (no events: declared) means no filtering — always true.
//   - An empty current event (unknown) also means no filtering — events are
//     enforced only when the event is known, so non-CI/manual paths stay lenient.
//   - Comparison is case-insensitive and trims surrounding whitespace.
//   - A push-equivalent event (manual or schedule — see pushEquivalent) satisfies a
//     "push" gate as well as its own literal gate name, so manual re-runs and
//     scheduled auto-release pipelines distribute exactly what a push would. The
//     relation is one-way: a plain "push" does NOT satisfy a manual-/schedule-only
//     gate.
func EventMatches(events []string, current string) bool {
	if len(events) == 0 || current == "" {
		return true
	}
	cur := strings.ToLower(strings.TrimSpace(current))
	for _, e := range events {
		le := strings.ToLower(strings.TrimSpace(e))
		if le == cur {
			return true
		}
		// A manual or scheduled run stands in for a push (mainline distribution).
		if le == "push" && pushEquivalent(cur) {
			return true
		}
	}
	return false
}
