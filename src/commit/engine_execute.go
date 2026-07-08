package commit

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// ConfirmRequiredError is returned when Execute reaches a Confirm without approval — the
// plan is sound, but a shared-history mutation needs an explicit yes.
type ConfirmRequiredError struct{ Detail string }

func (e *ConfirmRequiredError) Error() string { return "confirmation required: " + e.Detail }

// DecisionRequiredError is returned when Execute reaches a Decide — the planner has
// multiple safe paths and no policy to choose; the operator must pick.
type DecisionRequiredError struct {
	Detail  string
	Choices []string
}

func (e *DecisionRequiredError) Error() string {
	return fmt.Sprintf("decision required: %s (choose: %v)", e.Detail, e.Choices)
}

// RefusedError is returned when Execute reaches a Refuse.
type RefusedError struct{ Detail string }

func (e *RefusedError) Error() string { return "refused: " + e.Detail }

// ExecuteOptions carries what the graph-walker needs to satisfy interaction gates.
type ExecuteOptions struct {
	// Approved satisfies a Confirm gate (operator said yes, or a governed --yes). Without
	// it, Execute stops at the first Confirm and mutates nothing.
	Approved bool
}

// ExecuteResult records what actually happened.
type ExecuteResult struct {
	Performed []gitplan.OpKind
}

// Execute is the boring graph-walker: it walks the plan's operations in order and realizes
// each through the EXISTING session/transport (never a new git shell-out or go-git call).
// All decisions were made in Plan(); Execute only carries them out. The interaction ops
// gate: Refuse stops, Decide surfaces the choices, Confirm requires approval before the
// operations that follow it — so a shared-history mutation can never run un-gated.
func (e *Engine) Execute(plan gitplan.Plan, opts ExecuteOptions) (*ExecuteResult, error) {
	res := &ExecuteResult{}
	remote := plan.Dest.Remote
	for _, op := range plan.Operations {
		switch op.Kind {
		case gitplan.OpNoop:
			// nothing to do
		case gitplan.OpTeach:
			e.emitInfo(op.Detail)
		case gitplan.OpRefuse:
			return res, &RefusedError{Detail: op.Detail}
		case gitplan.OpDecide:
			return res, &DecisionRequiredError{Detail: op.Detail, Choices: op.Choices}
		case gitplan.OpConfirm:
			if !opts.Approved {
				return res, &ConfirmRequiredError{Detail: op.Detail}
			}
			// approved — fall through to the gated operations that follow
		case gitplan.OpCreateTracking:
			// Push the branch explicitly (HEAD → its ref) and set tracking. A bare push of a
			// no-upstream branch is rejected by system git ("no upstream branch"), so name the
			// destination ref — this is the first-push that actually creates the remote branch.
			branch := plan.Dest.Branch
			if branch == "" {
				branch = e.session.State().Branch
			}
			if err := e.session.Push(remote, "HEAD:refs/heads/"+branch, true); err != nil {
				return res, fmt.Errorf("create tracking: %w", err)
			}
		case gitplan.OpUpload:
			if err := e.session.Push(remote, "", false); err != nil {
				return res, fmt.Errorf("upload: %w", err)
			}
		case gitplan.OpDirectPush:
			// op.Detail carries the explicit refspec (CI detached-HEAD direct push).
			if err := e.session.Push(remote, op.Detail, false); err != nil {
				return res, fmt.Errorf("direct push: %w", err)
			}
		case gitplan.OpFastForward:
			if err := e.session.FastForward(remote); err != nil {
				return res, fmt.Errorf("fast-forward: %w", err)
			}
		case gitplan.OpReplay:
			if err := Replay(e.session); err != nil {
				return res, fmt.Errorf("replay: %w", err)
			}
		case gitplan.OpOfferMR:
			e.emitInfo("offer merge request: " + op.Detail) // real MR wiring is a later slice
		default:
			return res, fmt.Errorf("execute: unknown operation %q", op.Kind)
		}
		res.Performed = append(res.Performed, op.Kind)
	}
	return res, nil
}

// emitInfo surfaces an informational line through the existing event channel.
func (e *Engine) emitInfo(msg string) {
	if e.opts.OnEvent != nil {
		e.opts.OnEvent(gitstate.TransitionEvent{Action: "INFO", Note: msg})
	}
}
