package config

import (
	"strings"
	"testing"
)

// TestResolvePatternsVocabulary covers the full when: pattern vocabulary now
// single-sourced in ResolvePatterns: named policies, "re:" inline regex, and
// "!" negation — including negated NAMED patterns, which the old per-capability
// resolvers (docker's resolveWhenPatterns, release's resolveWhenPatternsFromCfg)
// mishandled by failing to resolve the policy before negating.
func TestResolvePatternsVocabulary(t *testing.T) {
	pol := map[string]string{"stable": `^v\d+\.\d+\.\d+$`, "main": `^main$`}

	cases := []struct {
		name     string
		patterns []string
		value    string
		want     bool
	}{
		{"named policy match", []string{"stable"}, "v1.2.3", true},
		{"named policy no match", []string{"stable"}, "dev", false},
		{"re: inline regex match", []string{"re:^release/.*$"}, "release/1.0", true},
		{"re: inline regex no match", []string{"re:^release/.*$"}, "main", false},
		{"re: regex with colon/metachars", []string{`re:^v\d+:beta$`}, "v3:beta", true},
		// The negated-named-pattern fix: "!stable" must resolve the policy THEN
		// negate. Old docker/release resolvers left "stable" literal, so a real
		// version slipped through the exclusion.
		{"negated named policy excludes match", []string{"!stable"}, "v1.2.3", false},
		{"negated named policy allows non-match", []string{"!stable"}, "dev", true},
		{"negated re: excludes", []string{"!re:^wip/.*$"}, "wip/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchPatternsWithPolicy(c.patterns, c.value, pol); got != c.want {
				t.Errorf("MatchPatternsWithPolicy(%v, %q) = %v, want %v", c.patterns, c.value, got, c.want)
			}
		})
	}
}

// TestTargetEligibilityReasons asserts the rejection reason is coupled to the
// decision (so narration cannot drift from the matcher) and that the TargetMatches
// bool wrapper agrees with TargetEligibility.Eligible.
func TestTargetEligibilityReasons(t *testing.T) {
	tagPol := map[string]string{"stable": `^v\d+\.\d+\.\d+$`}
	brPol := map[string]string{"main": `^main$`}
	dev := TargetConfig{When: WhenConditions{{Branches: []string{"main"}, Events: []string{"push"}}}}

	// Eligible: a real push on main carries no reason.
	if r := TargetEligibility(dev, "push", "main", "", "", tagPol, brPol); !r.Eligible || r.Reason != "" {
		t.Fatalf("push/main: want eligible with empty reason, got %+v", r)
	}

	// Ineligible: a manual web run. The reason must name both the run source and
	// the event gate so narration is self-explaining.
	r := TargetEligibility(dev, "web", "main", "", "", tagPol, brPol)
	if r.Eligible {
		t.Fatalf("web run must be ineligible for events:[push]")
	}
	if !strings.Contains(r.Reason, "web") || !strings.Contains(r.Reason, "events") {
		t.Errorf("event reason should name source and gate, got %q", r.Reason)
	}
	if TargetMatches(dev, "web", "main", "", "", tagPol, brPol) {
		t.Errorf("TargetMatches must agree with TargetEligibility.Eligible (false)")
	}

	// Branch mismatch surfaces a branch reason.
	if rb := TargetEligibility(dev, "push", "feature", "", "", tagPol, brPol); rb.Eligible || !strings.Contains(rb.Reason, "branch") {
		t.Errorf("feature branch: want ineligible with branch reason, got %+v", rb)
	}
}
