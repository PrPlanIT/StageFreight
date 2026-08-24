package config

import "testing"

func TestEventMatches(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		cur    string
		want   bool
	}{
		{"empty filter passes", nil, "push", true},
		{"empty current is lenient", []string{"tag"}, "", true},
		{"member push", []string{"push"}, "push", true},
		{"member among many", []string{"tag", "schedule"}, "tag", true},
		{"non-member rejected", []string{"tag"}, "push", false},
		{"case-insensitive", []string{"Push"}, "push", true},
		{"whitespace trimmed", []string{" push "}, "push", true},
		// Push-equivalent ⇒ push. EventMatches speaks canonical vocabulary — the raw
		// web/api/trigger/workflow_dispatch names are collapsed to "manual" by
		// NormalizeEvent before they reach here.
		{"manual satisfies push gate", []string{"push"}, "manual", true},
		{"manual satisfies manual gate", []string{"manual"}, "manual", true},
		// A scheduled auto-release pipeline also distributes like a push.
		{"schedule satisfies push gate", []string{"push"}, "schedule", true},
		{"schedule satisfies schedule gate", []string{"schedule"}, "schedule", true},
		// One-way: a plain push does NOT satisfy a manual-/schedule-only gate.
		{"push does not satisfy manual-only gate", []string{"manual"}, "push", false},
		{"push does not satisfy schedule-only gate", []string{"schedule"}, "push", false},
		// Push-equivalent does NOT satisfy a tag gate on a branch (tag context resolves
		// to event "tag" upstream, so a branch manual/schedule run is not a tag).
		{"manual does not satisfy tag gate", []string{"tag"}, "manual", false},
		// merge_request is a proposed change, not a mainline build — not push-equivalent.
		{"merge_request not push-equivalent", []string{"push"}, "merge_request", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EventMatches(c.events, c.cur); got != c.want {
				t.Errorf("EventMatches(%v, %q) = %v, want %v", c.events, c.cur, got, c.want)
			}
		})
	}
}

// TestNormalizeEvent locks in the raw-forge-source → canonical-vocabulary mapping,
// the single boundary that lets a portable gate (events:[manual]) match despite each
// forge naming the same trigger differently.
func TestNormalizeEvent(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		// Manual re-run has five raw names across forges — all → "manual".
		{"web", "manual"}, {"api", "manual"}, {"trigger", "manual"}, // GitLab
		{"workflow_dispatch", "manual"},              // GitHub/Gitea/Forgejo
		{"Manual", "manual"},                         // Azure (Build.Reason)
		{"manual", "manual"},                         // already canonical
		{"push", "push"},                             // GitLab/GitHub
		{"IndividualCI", "push"}, {"BatchedCI", "push"}, // Azure
		{"schedule", "schedule"}, {"Schedule", "schedule"}, // case-fold
		{"merge_request", "merge_request"},  // GitLab
		{"pull_request", "pull_request"},    // GitHub
		{"PullRequest", "pull_request"},     // Azure
		{"tag", "tag"}, {"release", "release"},
		{" Web ", "manual"},   // trimmed + folded
		{"external", "external"}, // unknown → passthrough lowercased (lenient)
		{"", ""},                 // empty stays empty
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			if got := NormalizeEvent(c.raw); got != c.want {
				t.Errorf("NormalizeEvent(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}
