package cmd

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

func TestPerformGate(t *testing.T) {
	cases := []struct {
		name   string
		c      *cistate.SubsystemState
		build  bool
		hasErr bool
	}{
		{"nil contract → fail-closed", nil, false, true},
		{"clean → build", &cistate.SubsystemState{Blocking: false}, true, false},
		{"blocked + replacement → skip (warn)", &cistate.SubsystemState{Blocking: true, Replacement: "c1"}, false, false},
		{"blocked, no replacement → fail", &cistate.SubsystemState{Blocking: true, Reason: "unremediable"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build, err := performGate(tc.c)
			if build != tc.build {
				t.Fatalf("build = %v, want %v", build, tc.build)
			}
			if (err != nil) != tc.hasErr {
				t.Fatalf("err = %v, want hasErr=%v", err, tc.hasErr)
			}
		})
	}

	// The safety property: a blocked contract NEVER yields build=true, whatever the lineage.
	t.Run("blocked never builds", func(t *testing.T) {
		for _, repl := range []string{"", "c1"} {
			if build, _ := performGate(&cistate.SubsystemState{Blocking: true, Replacement: repl}); build {
				t.Fatalf("blocked (replacement=%q) must never build", repl)
			}
		}
	})
}

func TestDeriveAuditionContract(t *testing.T) {
	good := auditionInputs{RunnerHealthy: true, TestsPassed: true}

	t.Run("clean is not blocking", func(t *testing.T) {
		c := deriveAuditionContract(good)
		if c.Blocking {
			t.Fatalf("clean must not block: %+v", c)
		}
		if c.Outcome != "success" {
			t.Fatalf("clean Outcome = %q, want success", c.Outcome)
		}
		if c.Replacement != "" {
			t.Fatalf("clean Replacement = %q, want empty", c.Replacement)
		}
	})

	// Each blocking condition, in isolation, must block.
	blockers := map[string]auditionInputs{
		"runner unhealthy":          {RunnerHealthy: false, TestsPassed: true},
		"fatal finding":             {RunnerHealthy: true, Fatal: true, TestsPassed: true},
		"remediable (remediate on)": {RunnerHealthy: true, Remediable: true, RemediationEnabled: true, TestsPassed: true},
		"tests failed":              {RunnerHealthy: true, TestsPassed: false},
		"deps errored":              {RunnerHealthy: true, TestsPassed: true, DepsErrored: true},
	}
	for name, in := range blockers {
		t.Run(name+" blocks", func(t *testing.T) {
			c := deriveAuditionContract(in)
			if !c.Blocking {
				t.Fatalf("%s must block: %+v", name, c)
			}
			if c.Outcome != "failed" {
				t.Fatalf("%s Outcome = %q, want failed", name, c.Outcome)
			}
		})
	}

	// THE invariant the whole design hinges on: a REMEDIATED source (fix committed as C′) is
	// STILL blocking — the fix is in the replacement, not in this subject. Replacement must
	// never flip Blocking to false. This is the exact correctness bug that was caught in review.
	t.Run("remediated is still blocking", func(t *testing.T) {
		in := auditionInputs{RunnerHealthy: true, Remediable: true, RemediationEnabled: true, TestsPassed: true, Replacement: "abc123"}
		c := deriveAuditionContract(in)
		if !c.Blocking {
			t.Fatalf("remediated source MUST stay blocking (fix is in C′, not here): %+v", c)
		}
		if c.Replacement != "abc123" {
			t.Fatalf("Replacement = %q, want abc123", c.Replacement)
		}
		if !strings.Contains(c.Reason, "abc123") {
			t.Fatalf("Reason should name the replacement: %q", c.Reason)
		}
		// Trustworthy badge: a self-healing remediation is a WARNING, not a hard failure.
		if !c.AllowFailure {
			t.Fatalf("remediated must be AllowFailure (badge = warning): %+v", c)
		}
	})

	t.Run("unremediable names human and fails the badge", func(t *testing.T) {
		in := auditionInputs{RunnerHealthy: true, Remediable: true, RemediationEnabled: true, TestsPassed: true}
		c := deriveAuditionContract(in)
		if !c.Blocking || c.Replacement != "" {
			t.Fatalf("unremediable: want blocking + no replacement: %+v", c)
		}
		if !strings.Contains(c.Reason, "resolve manually") {
			t.Fatalf("unremediable Reason should signal manual resolution: %q", c.Reason)
		}
		// Trustworthy badge: a dead-end (no fix) FAILS, not warns.
		if c.AllowFailure {
			t.Fatalf("unremediable must NOT be AllowFailure (badge = failing): %+v", c)
		}
	})

	// remediate: false — evaluate-only. A remediable finding with remediation DISABLED must
	// NOT block: the operator opted out of fix-forward, so the subject ships as-is (vuln gating,
	// if wanted, is the separate security.fail_on residual gate, not this contract). This is the
	// case that was wedging image-mode forks: audition passed but the subject dead-ended at
	// perform with "no automated fix".
	t.Run("remediable with remediation disabled does not block", func(t *testing.T) {
		in := auditionInputs{RunnerHealthy: true, Remediable: true, RemediationEnabled: false, TestsPassed: true}
		c := deriveAuditionContract(in)
		if c.Blocking {
			t.Fatalf("remediate:false remediable must NOT block (ship as-is): %+v", c)
		}
		if c.Outcome != "success" {
			t.Fatalf("remediate:false remediable Outcome = %q, want success", c.Outcome)
		}
	})

	// Exhaustive safety net over the 5 boolean facts (2^5). Blocking is false ONLY when nothing
	// blocks: healthy, no fatal, tests pass, deps did not error, and no remediable finding that
	// remediation is set to fix-forward — i.e. a remediable finding blocks IFF remediation is
	// enabled. (Replacement is lineage and must not affect Blocking, so it's fixed empty here and
	// checked separately above.)
	t.Run("blocking is false iff shippable", func(t *testing.T) {
		for _, healthy := range []bool{false, true} {
			for _, fatal := range []bool{false, true} {
				for _, rem := range []bool{false, true} {
					for _, remEnabled := range []bool{false, true} {
						for _, tests := range []bool{false, true} {
							for _, depsErr := range []bool{false, true} {
								in := auditionInputs{RunnerHealthy: healthy, Fatal: fatal, Remediable: rem, RemediationEnabled: remEnabled, TestsPassed: tests, DepsErrored: depsErr}
								shippable := healthy && !fatal && !(rem && remEnabled) && tests && !depsErr
								c := deriveAuditionContract(in)
								if c.Blocking == shippable {
									t.Fatalf("in=%+v: Blocking=%v but shippable=%v", in, c.Blocking, shippable)
								}
							}
						}
					}
				}
			}
		}
	})
}
