package commit

import "github.com/PrPlanIT/StageFreight/src/gitstate"

// Engine drives a repository push through the planner: Plan() (engine_plan.go) resolves the
// current repository state into an operation graph (gitplan.Plan), and Execute()
// (engine_execute.go) walks it, realizing each operation through the bound SyncSession —
// whose Transport (system git or embedded go-git) it inherits. The pre-planner state-machine
// convergence (Sync and its explicit transitions) has been retired in favour of the planner.
type Engine struct {
	session *gitstate.SyncSession
	opts    EngineOptions
}

// EngineOptions configures Engine behaviour.
type EngineOptions struct {
	// OnEvent receives a structured event for informational transitions (e.g. the Teach
	// lines emitted during Execute). If nil, events are dropped.
	OnEvent func(gitstate.TransitionEvent)
}

// NewEngine creates an Engine bound to the given SyncSession.
func NewEngine(session *gitstate.SyncSession, opts EngineOptions) *Engine {
	return &Engine{session: session, opts: opts}
}
