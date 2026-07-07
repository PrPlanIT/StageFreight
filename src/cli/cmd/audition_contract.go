package cmd

import (
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

// performGate is the PURE decision Perform makes from the audition contract. It returns
// whether to build, and — when not building — the error to render (nil = clean skip, which the
// superseding pipeline turns into a Warn; non-nil = a hard Fail). CONTROL is `Blocking` alone;
// the nil-vs-error choice is the RENDER, driven by Replacement, and never gates the build. A
// nil contract is fail-closed: refuse to build unaudited source.
func performGate(c *cistate.SubsystemState) (build bool, err error) {
	if c == nil {
		return false, fmt.Errorf("audition contract missing — refusing to build unaudited source")
	}
	if !c.Blocking {
		return true, nil
	}
	if c.Replacement != "" {
		return false, nil // superseded by a fix → skip clean (Warn)
	}
	return false, fmt.Errorf("source blocked by audition — %s", c.Reason) // dead-end → Fail
}

// auditionContract reads the audition contract from the ledger, or nil if none exists (an
// unreadable ledger, or a lifecycle mode that publishes no audition contract). Callers treat
// nil as fail-closed where a contract is expected.
func auditionContract(rootDir string) *cistate.SubsystemState {
	st, err := cistate.ReadState(rootDir)
	if err != nil {
		return nil
	}
	return st.GetSubsystem("audition")
}

// narrateAuditionLineage prints a human explanation of the audition contract — what happened
// to the subject and what supersedes it. Communication only: it reads the ledger and never
// gates. Silent when the subject was acceptable (nothing to explain).
func narrateAuditionLineage(rootDir string) {
	c := auditionContract(rootDir)
	if c == nil || !c.Blocking {
		return
	}
	if c.Replacement != "" {
		fmt.Printf("  narrate: source auto-remediated — candidate %s created; its pipeline ships the fix.\n", c.Replacement)
		return
	}
	fmt.Printf("  narrate: source blocked — %s.\n", c.Reason)
}

// recordAuditionContract writes the audition contract into the ledger — the single
// authoritative record downstream phases gate on (Blocking) and narrate/publish/the forge
// renderer project (Replacement, Reason, Outcome). Recorded UNCONDITIONALLY (not CI-gated) so
// a local `stagefreight run` gates on it exactly as a forge pipeline does — the control path
// never depends on the forge.
func recordAuditionContract(rootDir string, contract cistate.SubsystemState) {
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(contract)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: audition contract write failed: %v\n", err)
	}
}

// auditionInputs are the observed, forge-independent facts of one audition run over its
// subject (the triggering source). deriveAuditionContract projects them into the single
// authoritative `audition` contract the rest of the pipeline consumes. Keeping this a pure
// function makes the safety-critical logic unit-testable with no CI, no forge, no I/O.
type auditionInputs struct {
	RunnerHealthy bool   // executor substrate healthy
	Fatal         bool   // a Fatal lint finding (secret / conflict / broken tree) — voids the source
	Remediable    bool   // a Remediable blocking finding (freshness/osv CVE) on the source
	TestsPassed   bool   // the audition correctness gate (committed-tree tests) passed
	DepsErrored   bool   // the deps update itself errored
	Replacement   string // the fix commit (C′) if remediation committed one, else ""
}

// deriveAuditionContract is the PURE projection of an audition run into its contract. Two
// invariants it must never violate:
//
//   - Blocking answers only "is this subject shippable?" It is false ONLY when nothing blocks:
//     runner healthy, no fatal, no remediable finding, tests passed, deps did not error. A
//     REMEDIATED source stays Blocking — the fix is in Replacement (C′), NOT in this subject,
//     so building this subject would ship unfixed source.
//   - Replacement is lineage only. It records the fix commit but NEVER makes a subject
//     non-Blocking. Control (Perform) reads Blocking; it must never read Replacement.
func deriveAuditionContract(in auditionInputs) cistate.SubsystemState {
	blocking := !in.RunnerHealthy || in.Fatal || in.Remediable || !in.TestsPassed || in.DepsErrored

	c := cistate.SubsystemState{
		Name:        "audition",
		Attempted:   true,
		Completed:   true,
		Blocking:    blocking,
		Replacement: in.Replacement,
		// AllowFailure drives the BADGE only (PipelineStatus), never control. A blocking subject
		// that produced a fix (Replacement) self-heals → the badge is a WARNING; a blocking
		// subject with no fix is a dead-end → the badge FAILS. So narrate ships a trustworthy
		// badge: passing (clean) / warning (remediated) / failing (unremediable or errored).
		AllowFailure: in.Replacement != "",
	}
	if !blocking {
		c.Outcome = "success"
		c.Reason = "candidate acceptable"
		return c
	}
	c.Outcome = "failed"
	// Reason is human/lineage text only — it never affects control. Most-fundamental first.
	switch {
	case !in.RunnerHealthy:
		c.Reason = "runner substrate unhealthy"
	case in.Fatal:
		c.Reason = "fatal finding voids the source (secret, merge conflict, or broken tree); resolve manually and re-run"
	case in.DepsErrored:
		c.Reason = "dependency update errored"
	case !in.TestsPassed:
		c.Reason = "committed-tree tests failed"
	case in.Replacement != "":
		c.Reason = "remediated — superseded by " + in.Replacement
	default: // Remediable, no fix produced
		c.Reason = "blocking finding has no automated fix; resolve manually and re-run"
	}
	return c
}
