package commit

import (
	"fmt"
	"strconv"
	"strings"
)

// SyncAction names a single step in the repository convergence plan.
type SyncAction string

const (
	SyncSetUpstream SyncAction = "set-upstream"  // configure tracking branch on first push
	SyncFetch       SyncAction = "fetch"          // fetch remote before rebase or fast-forward
	SyncFastForward SyncAction = "fast-forward"   // merge --ff-only to catch up to upstream
	SyncRebase      SyncAction = "rebase"         // rebase local commits onto upstream
	SyncPush        SyncAction = "push"           // push to remote
	SyncNoop        SyncAction = "noop"           // already up to date, nothing to do
)

// RepoState is the result of interrogating the current repository condition.
// DetectRepoState always returns a fully populated struct — callers must check
// DetachedHEAD and UpstreamConfigured before interpreting other fields.
type RepoState struct {
	Branch             string // current branch name (empty if DetachedHEAD)
	UpstreamRef        string // e.g. "origin/main" — empty if not configured
	UpstreamConfigured bool
	AheadCount         int  // commits local has that remote does not
	BehindCount        int  // commits remote has that local does not
	DetachedHEAD       bool
}

// Diverged returns true when local and remote have independent commits.
func (s RepoState) Diverged() bool {
	return s.AheadCount > 0 && s.BehindCount > 0
}

// SyncStep is one unit of work in a SyncPlan.
type SyncStep struct {
	Action SyncAction
	Reason string // human-readable rationale for this step
}

// SyncPlan is the resolved sequence of actions to converge local with remote.
type SyncPlan struct {
	Steps           []SyncStep
	Remote          string
	Refspec         string // optional explicit refspec; empty = current branch
	RebaseOnDiverge bool   // when true, rebase instead of failing on diverge
}

// SyncResult is the outcome after executing a SyncPlan.
type SyncResult struct {
	ActionsExecuted []SyncAction
	PushedRef       string // remote name that was pushed to
	Noop            bool   // true only when SyncNoop was the sole action
}

// DetectRepoState interrogates git to produce the current RepoState.
// This is always called just before push — after the commit has landed —
// so AheadCount will be at least 1 for a normal commit+push.
func (g *GitBackend) DetectRepoState() (RepoState, error) {
	var state RepoState

	// Detached HEAD check: symbolic-ref exits non-zero on detached HEAD.
	headRef, err := g.gitOutput("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		state.DetachedHEAD = true
		return state, nil
	}

	// Extract branch name from refs/heads/main → main.
	state.Branch = strings.TrimPrefix(headRef, "refs/heads/")

	// Check for upstream tracking configuration.
	upstream, err := g.gitOutput("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		// No upstream — normal for a freshly created branch.
		state.UpstreamConfigured = false
		return state, nil
	}
	state.UpstreamConfigured = true
	state.UpstreamRef = upstream

	// ahead/behind counts: git rev-list --count --left-right @{u}...HEAD
	// Output: "N\tM" where N = behind (remote has), M = ahead (local has).
	counts, err := g.gitOutput("rev-list", "--count", "--left-right", "@{u}...HEAD")
	if err != nil {
		// Non-fatal: leave counts at zero if the upstream ref is inaccessible.
		return state, nil
	}
	parts := strings.Fields(counts)
	if len(parts) == 2 {
		state.BehindCount, _ = strconv.Atoi(parts[0])
		state.AheadCount, _ = strconv.Atoi(parts[1])
	}

	return state, nil
}

// PlanSync produces a deterministic sequence of SyncSteps to push the current
// branch to remote, handling divergence, missing upstream, and up-to-date states.
//
// rebaseOnDiverge controls behaviour when the branch has diverged: when true the
// plan includes a rebase step; when false PlanSync returns an error describing
// what manual action is required.
func PlanSync(state RepoState, remote, refspec string, rebaseOnDiverge bool) (SyncPlan, error) {
	plan := SyncPlan{Remote: remote, Refspec: refspec, RebaseOnDiverge: rebaseOnDiverge}

	if state.DetachedHEAD {
		return plan, fmt.Errorf("push refused: detached HEAD — checkout a named branch first")
	}

	if !state.UpstreamConfigured {
		// First push for this branch — set tracking on the way through.
		plan.Steps = append(plan.Steps,
			SyncStep{Action: SyncSetUpstream, Reason: "no upstream tracking branch configured"},
			SyncStep{Action: SyncPush, Reason: "push and configure tracking"},
		)
		return plan, nil
	}

	switch {
	case state.Diverged():
		if !rebaseOnDiverge {
			return plan, fmt.Errorf(
				"push refused: branch %q has diverged from %s (%d ahead, %d behind) — "+
					"run: git pull --rebase %s",
				state.Branch, state.UpstreamRef, state.AheadCount, state.BehindCount, remote,
			)
		}
		plan.Steps = append(plan.Steps,
			SyncStep{Action: SyncFetch, Reason: fmt.Sprintf("fetch before rebase (%d ahead, %d behind %s)", state.AheadCount, state.BehindCount, state.UpstreamRef)},
			SyncStep{Action: SyncRebase, Reason: fmt.Sprintf("rebase onto %s", state.UpstreamRef)},
			SyncStep{Action: SyncPush, Reason: "push rebased commits"},
		)

	case state.BehindCount > 0 && state.AheadCount == 0:
		// Behind only: fetch then fast-forward to catch up, then push (will be noop).
		// This handles the unusual case where a local commit was just undone
		// or reverted but upstream moved forward.
		plan.Steps = append(plan.Steps,
			SyncStep{Action: SyncFetch, Reason: fmt.Sprintf("fetch before fast-forward (%d commit(s) behind %s)", state.BehindCount, state.UpstreamRef)},
			SyncStep{Action: SyncFastForward, Reason: fmt.Sprintf("fast-forward to %s", state.UpstreamRef)},
			SyncStep{Action: SyncPush, Reason: "push (up to date after fast-forward)"},
		)

	case state.AheadCount > 0:
		// Ahead only — straightforward push.
		plan.Steps = append(plan.Steps,
			SyncStep{Action: SyncPush, Reason: fmt.Sprintf("push %d commit(s)", state.AheadCount)},
		)

	default:
		// Already up to date.
		plan.Steps = append(plan.Steps,
			SyncStep{Action: SyncNoop, Reason: "already up to date with " + state.UpstreamRef},
		)
	}

	return plan, nil
}

// Sync executes a SyncPlan against the repository.
// Steps are executed in order; the first failure aborts the plan and returns
// the partial SyncResult alongside the error.
func (g *GitBackend) Sync(plan SyncPlan) (*SyncResult, error) {
	result := &SyncResult{}

	for _, step := range plan.Steps {
		switch step.Action {

		case SyncNoop:
			result.Noop = true
			result.ActionsExecuted = append(result.ActionsExecuted, SyncNoop)

		case SyncSetUpstream:
			// Handled during SyncPush via --set-upstream-to; record intent here.
			result.ActionsExecuted = append(result.ActionsExecuted, SyncSetUpstream)

		case SyncFetch:
			if err := g.git("fetch", plan.Remote); err != nil {
				return result, fmt.Errorf("fetch %s: %w", plan.Remote, err)
			}
			result.ActionsExecuted = append(result.ActionsExecuted, SyncFetch)

		case SyncFastForward:
			if err := g.git("merge", "--ff-only", "@{u}"); err != nil {
				return result, fmt.Errorf("fast-forward to upstream: %w", err)
			}
			result.ActionsExecuted = append(result.ActionsExecuted, SyncFastForward)

		case SyncRebase:
			if err := g.git("rebase", "@{u}"); err != nil {
				return result, fmt.Errorf("rebase onto upstream: %w\n  hint: resolve conflicts, then run: git rebase --continue", err)
			}
			result.ActionsExecuted = append(result.ActionsExecuted, SyncRebase)

		case SyncPush:
			pushArgs := []string{"push"}
			if containsAction(result.ActionsExecuted, SyncSetUpstream) {
				pushArgs = append(pushArgs, "--set-upstream")
			}
			pushArgs = append(pushArgs, plan.Remote)
			if plan.Refspec != "" {
				pushArgs = append(pushArgs, plan.Refspec)
			}
			if err := g.git(pushArgs...); err != nil {
				return result, fmt.Errorf("push to %s: %w", plan.Remote, err)
			}
			result.ActionsExecuted = append(result.ActionsExecuted, SyncPush)
			result.PushedRef = plan.Remote
		}
	}

	return result, nil
}

// extractRemote returns the remote name from an upstream ref like "origin/main".
func extractRemote(upstreamRef string) string {
	if idx := strings.Index(upstreamRef, "/"); idx >= 0 {
		return upstreamRef[:idx]
	}
	return "origin"
}

// containsAction returns true if action appears in the slice.
func containsAction(actions []SyncAction, action SyncAction) bool {
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}
