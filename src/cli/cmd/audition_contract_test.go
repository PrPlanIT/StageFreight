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
		"runner unhealthy": {RunnerHealthy: false, TestsPassed: true},
		"fatal finding":    {RunnerHealthy: true, Fatal: true, TestsPassed: true},
		"remediable":       {RunnerHealthy: true, Remediable: true, TestsPassed: true},
		"tests failed":     {RunnerHealthy: true, TestsPassed: false},
		"deps errored":     {RunnerHealthy: true, TestsPassed: true, DepsErrored: true},
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
		in := auditionInputs{RunnerHealthy: true, Remediable: true, TestsPassed: true, Replacement: "abc123"}
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
		in := auditionInputs{RunnerHealthy: true, Remediable: true, TestsPassed: true}
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

	// Exhaustive safety net: Blocking is false for exactly ONE input combination — all-good.
	// Any single degraded input must block. (2^4 over the boolean facts; Replacement is lineage
	// and must not affect Blocking, so it's fixed empty here and checked separately above.)
	t.Run("blocking is false iff everything is good", func(t *testing.T) {
		for _, healthy := range []bool{false, true} {
			for _, fatal := range []bool{false, true} {
				for _, rem := range []bool{false, true} {
					for _, tests := range []bool{false, true} {
						for _, depsErr := range []bool{false, true} {
							in := auditionInputs{RunnerHealthy: healthy, Fatal: fatal, Remediable: rem, TestsPassed: tests, DepsErrored: depsErr}
							allGood := healthy && !fatal && !rem && tests && !depsErr
							c := deriveAuditionContract(in)
							if c.Blocking == allGood {
								t.Fatalf("in=%+v: Blocking=%v but allGood=%v", in, c.Blocking, allGood)
							}
						}
					}
				}
			}
		}
	})
}
