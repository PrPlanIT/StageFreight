package commit

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// pushViaPlanner routes a branch push through the shared planner (Plan → Execute) in
// AUTO-CONVERGE mode, so `commit --push` uses the same push implementation as
// `stagefreight push` while preserving the pre-planner engine.Sync behaviour (fast-forward
// when behind, rebase-then-push when diverged). It is authorized to satisfy the Confirm
// gate the converge plan places before a replay.
func (g *GitBackend) pushViaPlanner(opts PushOptions) (*SyncResult, error) {
	session, err := gitstate.OpenSyncSession(g.RootDir)
	if err != nil {
		return nil, fmt.Errorf("opening sync session: %w", err)
	}
	remote := opts.Remote
	if remote == "" {
		remote = "origin"
	}
	if err := session.Fetch(remote); err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	eng := NewEngine(session, EngineOptions{OnEvent: g.onSyncEvent})
	policy := gitplan.Policy{Protected: []string{"main", "master"}}
	plan := gitplan.Resolve(gitplan.SituationFromStateConverge(session.State(), policy))
	res, err := eng.Execute(plan, ExecuteOptions{Approved: true})
	if err != nil {
		return nil, err
	}
	return syncResultFromOps(res.Performed, remote), nil
}

// syncResultFromOps maps executed planner operations onto the legacy SyncResult shape so
// the commit command's push-status rendering is unchanged during the convergence.
func syncResultFromOps(ops []gitplan.OpKind, remote string) *SyncResult {
	r := &SyncResult{PushedRef: remote}
	for _, op := range ops {
		switch op {
		case gitplan.OpReplay:
			r.ActionsExecuted = append(r.ActionsExecuted, SyncRebase)
		case gitplan.OpFastForward:
			r.ActionsExecuted = append(r.ActionsExecuted, SyncFastForward)
		case gitplan.OpCreateTracking:
			r.ActionsExecuted = append(r.ActionsExecuted, SyncSetUpstream, SyncPush)
		case gitplan.OpUpload:
			r.ActionsExecuted = append(r.ActionsExecuted, SyncPush)
		case gitplan.OpNoop:
			r.ActionsExecuted = append(r.ActionsExecuted, SyncNoop)
		}
	}
	if len(r.ActionsExecuted) == 1 && r.ActionsExecuted[0] == SyncNoop {
		r.Noop = true
	}
	return r
}
