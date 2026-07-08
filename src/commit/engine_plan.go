package commit

import "github.com/PrPlanIT/StageFreight/src/gitplan"

// Plan resolves the current repository situation into a gitplan.Plan WITHOUT mutating —
// the read-only half of the planner/executor split. It reads the session's current state
// (reflecting the last fetch), folds in policy, and returns the operation graph that the
// render / execute consumers walk. All decision logic lives in the pure gitplan.Resolve;
// this is thin, deterministic glue over the existing session.
func (e *Engine) Plan(policy gitplan.Policy) gitplan.Plan {
	state := e.session.State()
	return gitplan.Resolve(gitplan.SituationFromState(state, policy))
}

// PlanPull resolves the current state into a DOWN-sync plan (`stagefreight pull`): bring the
// remote's commits into the local branch (fast-forward, or rebase-local-onto-remote when
// diverged). Read-only; the same executor walks it.
func (e *Engine) PlanPull(policy gitplan.Policy) gitplan.Plan {
	state := e.session.State()
	return gitplan.ResolvePull(gitplan.SituationFromState(state, policy))
}
