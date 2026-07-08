package gitplan

import "github.com/PrPlanIT/StageFreight/src/gitstate"

// SituationFromState builds a Situation for a push to the branch's OWN tracked upstream
// (the `sf push` default): Ahead/Behind come straight from RepoState — already relative to
// the upstream — and destination protected-ness comes from policy. Pure over RepoState (a
// plain struct), so it is fully unit-testable with no git.
//
// Pushing to an explicitly-named different branch (feat → main) computes destination-
// relative counts separately (a later slice) and builds a Situation the same way, so the
// pure planner never learns which case it is.
func SituationFromState(state gitstate.RepoState, policy Policy) Situation {
	remote := state.RemoteName
	if remote == "" {
		remote = "origin"
	}
	dest := Destination{
		Remote:    remote,
		Branch:    state.Branch,
		Protected: policy.IsProtected(state.Branch),
	}
	return Situation{
		Dest:         dest,
		HasUpstream:  state.UpstreamConfigured,
		Ahead:        state.AheadCount,
		Behind:       state.BehindCount,
		OnDiverge:    policy.DivergeRule(state.Branch),
		InProgressOp: state.InProgressOp,
	}
}

// SituationFromStateConverge is SituationFromState for the CI / `commit --push` auto-flow:
// an authorized context that converges (fast-forward when behind, replay when diverged)
// rather than asking. It is the single seam that lets `commit --push` share the planner
// while preserving the pre-planner engine.Sync behavior.
func SituationFromStateConverge(state gitstate.RepoState, policy Policy) Situation {
	s := SituationFromState(state, policy)
	s.AutoConverge = true
	return s
}
