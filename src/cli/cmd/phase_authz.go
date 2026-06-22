package cmd

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
)

// Phase authorization — the in-process gate that decides whether a phase is
// permitted to ACT, distinct from the GitLab DAG that decides whether it may
// EXECUTE.
//
// The pipeline marks the lifecycle jobs allow_failure:true so execution
// continues for observability — narrate/reporting always run, diagnostics
// survive failures. That continuation is correct ONLY when each phase
// self-authorizes: it refuses to perform its action if a required upstream
// phase recorded a genuine failure. This file is that authorization layer.
//
// Critically, authorization reads the RAW recorded Outcome and is INDEPENDENT
// of AllowFailure. A failed review must block publish even though the review
// job is allow_failure:true — that decoupling (scheduler continuation vs action
// authorization) is the whole point. cistate.PipelineStatus() is the wrong
// signal here precisely because it is AllowFailure-aware (scheduler-aligned);
// the gate is deliberately stricter.
//
// Only a genuine failure blocks. failed/cancelled deny authorization;
// success/warning/skipped/not_applicable all pass — honest classification means
// a degraded-but-evaluable upstream still authorizes, and only "could not
// evaluate / did fail" stops the irreversible action downstream.

// phaseUpstream maps a phase to the cistate subsystem outcomes that must not
// have failed for it to be authorized. The subsystem names are what the
// upstream phase runners record: perform → "build", review → "security".
//
// publish is the irreversible boundary (it pushes images and cuts releases), so
// it requires BOTH the build that produced the bytes and the review that
// evaluated them. review/perform are intentionally absent: review has its own
// deliberate proceed-on-build-failure behavior (scan fails naturally with a
// better error), and perform→audition gating is deferred until the audition
// allow_failure / lint-blocking question is settled — audition records no
// gateable subsystem today.
//
// Add an entry ONLY for a phase whose action is irreversible, externally
// mutating, or trust/provenance-bearing — publish today, and later the likes of
// signing, release creation, or deployment promotion. This is NOT a gate on
// every transition: ordinary phases rely on scheduler flow (allow_failure), and
// gating them merely because they exist rebuilds the over-engineered gate graph
// this design deliberately avoids. The clean boundary is: scheduler continuation
// decides execution flow; authorization decides whether irreversible actions may
// occur. New entries are a one-line addition.
var phaseUpstream = map[string][]string{
	"publish": {"build", "security"},
}

// authorizePhase returns an error (fail-closed) if any required upstream
// subsystem for phase did not pass. It is enforced only in CI, because the
// subsystem outcomes it reads are recorded only in CI (local runs have no
// accumulated state to authorize against, and authorizing against an empty
// state would wrongly block every local invocation).
func authorizePhase(ciCtx *ci.CIContext, rootDir, phase string) error {
	required := phaseUpstream[phase]
	if len(required) == 0 {
		return nil
	}
	if ciCtx == nil || !ciCtx.IsCI() {
		return nil
	}

	st, err := cistate.ReadState(rootDir)
	if err != nil {
		// No readable accumulated state in CI means the upstream evidence did
		// not propagate — fail closed rather than externalize blind.
		return fmt.Errorf("%s blocked: pipeline state unreadable (%v) — authorization denied", phase, err)
	}

	for _, name := range required {
		s := st.GetSubsystem(name)
		if s == nil {
			return fmt.Errorf("%s blocked: required upstream %q did not run — authorization denied", phase, name)
		}
		switch s.Outcome {
		case "success", "warning", "skipped", "not_applicable":
			// Authorized: an upstream that passed, warned, was skipped, or did
			// not apply does not block the transition. Honest classification
			// means only a genuine failure stops the irreversible action.
		case "failed", "cancelled":
			reason := s.Reason
			if reason == "" {
				reason = s.Outcome
			}
			return fmt.Errorf("%s blocked: upstream %q did not pass (%s) — authorization denied", phase, name, reason)
		default:
			// Allowlist, not denylist: an unrecognized or incomplete outcome
			// (e.g. recorded attempted but never reaching a terminal state) is
			// denied, never assumed safe.
			return fmt.Errorf("%s blocked: upstream %q has no conclusive outcome (%q) — authorization denied", phase, name, s.Outcome)
		}
	}
	return nil
}
