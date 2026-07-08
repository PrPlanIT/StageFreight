// Package gitplan is StageFreight's planner for shared-history git operations.
//
// It is the pure heart of "the plan IS the UX": given a fully-resolved Situation
// (repository State + Destination + Policy, folded into destination-relative facts),
// Resolve produces a Plan — an ordered graph of Operations (StageFreight verbs, not
// git verbs). Every consumer — render, explain, audit, simulate, execute — walks that
// one graph, so they can never diverge.
//
// Resolve is PURE: no I/O, no clock, no network. That is what makes the hard decisions
// (which operations, which gates) exhaustively unit-testable with zero git. All I/O
// lives in the resolver that builds a Situation and in the executor that walks the Plan.
package gitplan

import "fmt"

// OpKind is a StageFreight operation primitive. Interaction (Teach/Confirm/Decide) is
// itself an operation in the sequenced graph, not an attribute — so Execute and every
// other consumer just walk the graph; none has to remember how Confirm gates Replay.
type OpKind string

const (
	OpNoop           OpKind = "noop"
	OpUpload         OpKind = "upload"          // push commits to a fast-forwardable ref
	OpCreateTracking OpKind = "create-tracking" // first push: push + set upstream
	OpFastForward    OpKind = "fast-forward"    // advance local to a strictly-ahead remote
	OpReplay         OpKind = "replay"          // rebase local commits onto the destination (new SHAs)
	OpOfferMR        OpKind = "offer-mr"        // offer to open a merge request
	OpRefuse         OpKind = "refuse"          // stop: no safe/allowed action
	OpDirectPush     OpKind = "direct-push"     // push HEAD straight to an explicit refspec, no reconcile (CI detached-HEAD)

	// Interaction operations — sequenced in the graph, never attributes.
	OpTeach   OpKind = "teach"   // explain; execution continues
	OpConfirm OpKind = "confirm" // require a yes before the operations that follow
	OpDecide  OpKind = "decide"  // present choices; the operator picks
)

// mutatesSharedHistory reports whether an op rewrites/enters shared history and must
// therefore be gated by a preceding Confirm. Enforced as a structural invariant.
func (k OpKind) mutatesSharedHistory() bool {
	switch k {
	case OpReplay:
		return true
	}
	return false
}

// Operation is one node in the plan graph.
type Operation struct {
	Kind    OpKind
	Detail  string   // human description ("replay 3 commits onto origin/main")
	Reason  string   // required for governed/escape-hatch ops; captured to the audit trail
	Choices []string // for OpDecide
}

// Destination is where commits are going, with protected-ness resolved from policy.
type Destination struct {
	Remote    string
	Branch    string
	Protected bool // resolved from policy — the switch that "wakes" replay-onto-trunk
}

// Ref renders the destination as "remote/branch".
func (d Destination) Ref() string { return d.Remote + "/" + d.Branch }

// DivergeRule is the policy-resolved handling of a diverged NON-protected destination.
// Empty means "no policy" → the planner must ask (Decide), never guess: a diverged
// feature branch is user-intent ambiguity (pull vs rebase vs force), not policy's call.
type DivergeRule string

const (
	DivergeAsk    DivergeRule = ""       // user-intent ambiguity — Decide
	DivergeRebase DivergeRule = "rebase" // repo policy says linearize — Confirm→Replay
)

// Situation is the fully-resolved, destination-relative input to the pure planner.
// A resolver (impure, reads git + policy) builds this; Resolve consumes it. Ahead/Behind
// are relative to the DESTINATION ref, so the same planner serves "push to my own
// upstream" and "land on a protected branch" without knowing which it is.
type Situation struct {
	Dest        Destination
	HasUpstream bool        // is a tracking ref configured for this push path?
	Ahead       int         // local commits the destination lacks
	Behind      int         // destination commits the local lacks
	OnDiverge   DivergeRule // policy for a diverged non-protected destination

	// AutoConverge is the CI / `commit --push` context: an authorized auto-flow that
	// converges rather than asking. It makes `behind` fast-forward (not Refuse+Teach) and
	// any `diverged` replay (not Decide) — matching the pre-planner engine.Sync behavior.
	// Interactive `sf push` leaves it false. The Confirm gate is still emitted for shared
	// mutations; the auto-flow satisfies it via ExecuteOptions.Approved.
	AutoConverge bool

	// InProgressOp names a mid-flight git operation (merge/rebase/cherry-pick/revert). When
	// non-empty the planner refuses with guidance — StageFreight is a first-class git citizen
	// and never acts on a half-finished state (this is the "silent flatten" fix).
	InProgressOp string
}

// InteractionLevel is DERIVED from which interaction op the graph contains — it is
// execution policy (how autonomously the plan may run), not planner certainty.
type InteractionLevel string

const (
	Automatic InteractionLevel = "automatic" // no interaction op
	Inform    InteractionLevel = "inform"    // Teach present
	Confirm   InteractionLevel = "confirm"   // Confirm present
	Decide    InteractionLevel = "decide"    // Decide present
)

// Plan is the durable IR: an ordered operation graph plus metadata. Consumers grow by
// reading metadata; do not thread bare []Operation through APIs.
type Plan struct {
	Operations []Operation
	Dest       Destination
	Summary    string
	// room to grow without touching the operation list:
	// audit id, policy version, estimated mutations, provenance, timing, forge info.
}

// Interaction derives the interaction level from the graph (most-involved op present).
func (p Plan) Interaction() InteractionLevel {
	level := Automatic
	for _, op := range p.Operations {
		switch op.Kind {
		case OpDecide:
			return Decide // most-involved; short-circuit
		case OpConfirm:
			level = Confirm
		case OpTeach:
			if level == Automatic {
				level = Inform
			}
		}
	}
	return level
}

// Resolve is the pure planner: a Situation → a Plan. Deterministic — the same Situation
// always yields the same Plan (no clock, no randomness). This is the whole "intelligence":
// everything downstream is a consumer of the graph it returns.
func Resolve(s Situation) Plan {
	// First-class git citizen: never act on a half-finished git operation — refuse, explain.
	if s.InProgressOp != "" {
		return newPlan(s, "git operation in progress",
			Operation{Kind: OpRefuse, Detail: fmt.Sprintf("a git %s is in progress", s.InProgressOp)},
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("finish or abort it first (e.g. `git %s --abort`), then retry — StageFreight won't act on a half-finished state", s.InProgressOp)},
		)
	}
	switch {
	case !s.HasUpstream:
		return firstPush(s)
	case s.Ahead == 0 && s.Behind == 0:
		return newPlan(s, "up to date", Operation{Kind: OpNoop})
	case s.Ahead > 0 && s.Behind == 0:
		return newPlan(s, "upload", Operation{
			Kind:   OpUpload,
			Detail: fmt.Sprintf("upload %d commit(s) to %s", s.Ahead, s.Dest.Ref()),
		})
	case s.Ahead == 0 && s.Behind > 0:
		return behind(s)
	default: // ahead > 0 && behind > 0
		return diverged(s)
	}
}

func firstPush(s Situation) Plan {
	ops := []Operation{
		{Kind: OpTeach, Detail: fmt.Sprintf("no upstream — creating %s and setting tracking", s.Dest.Ref())},
		{Kind: OpCreateTracking, Detail: s.Dest.Ref()},
		{Kind: OpUpload},
	}
	if !s.Dest.Protected {
		ops = append(ops, Operation{Kind: OpOfferMR, Detail: s.Dest.Ref()})
	}
	return newPlan(s, "first push", ops...)
}

func behind(s Situation) Plan {
	// CI / commit --push auto-flow catches up by fast-forwarding, as engine.Sync did.
	if s.AutoConverge {
		return newPlan(s, "fast-forward", Operation{Kind: OpFastForward, Detail: s.Dest.Ref()})
	}
	return newPlan(s, "behind — pull first",
		Operation{Kind: OpRefuse, Detail: fmt.Sprintf("%s is %d commit(s) ahead of you", s.Dest.Ref(), s.Behind)},
		Operation{Kind: OpTeach, Detail: "remote has commits you don't; upload would overwrite newer work — run `sf pull`"},
	)
}

func diverged(s Situation) Plan {
	// Protected destination, a repo policy that says linearize, or the CI/commit-push
	// auto-flow → replay onto the destination, GATED by a Confirm (never a silent rewrite;
	// the auto-flow satisfies the Confirm via ExecuteOptions.Approved).
	if s.Dest.Protected || s.OnDiverge == DivergeRebase || s.AutoConverge {
		return newPlan(s, "replay onto destination",
			Operation{Kind: OpConfirm, Detail: fmt.Sprintf("replay %d commit(s) onto %s — new commit IDs", s.Ahead, s.Dest.Ref())},
			Operation{Kind: OpReplay, Detail: s.Dest.Ref()},
			Operation{Kind: OpUpload},
		)
	}
	// Non-protected feature branch with no policy: this is user-intent ambiguity, not
	// policy's to resolve. Decide — the planner never silently replays a feature branch.
	return newPlan(s, "diverged — choose",
		Operation{
			Kind:    OpDecide,
			Detail:  fmt.Sprintf("%s diverged (%d ahead, %d behind)", s.Dest.Ref(), s.Ahead, s.Behind),
			Choices: []string{"pull", "rebase", "force-with-lease"},
		},
	)
}

func newPlan(s Situation, summary string, ops ...Operation) Plan {
	return Plan{Operations: ops, Dest: s.Dest, Summary: summary}
}

// DirectPush is the CI detached-HEAD / explicit-refspec case: push HEAD straight to the
// given refspec with no fetch, classify, or reconcile — a trivial single-op plan that does
// not fit the ahead/behind model. It replaces the pre-planner engine.Sync refspec fast path.
func DirectPush(remote, refspec string) Plan {
	return Plan{
		Operations: []Operation{{Kind: OpDirectPush, Detail: refspec}},
		Dest:       Destination{Remote: remote},
		Summary:    "direct push (refspec)",
	}
}
