package gitplan

import "fmt"

// ResolvePull is the pure planner for down-sync (`stagefreight pull`): bring the remote's
// commits into the local branch. It never pushes — same operation vocabulary, other
// direction. Behind → fast-forward; diverged → rebase local onto the remote (Replay),
// gated by Confirm because it rewrites local commits; ahead/synced → nothing to pull.
func ResolvePull(s Situation) Plan {
	if s.InProgressOp != "" {
		return newPlan(s, "git operation in progress",
			Operation{Kind: OpRefuse, Detail: fmt.Sprintf("a git %s is in progress", s.InProgressOp)},
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("finish or abort it first (e.g. `git %s --abort`), then retry", s.InProgressOp)},
		)
	}
	switch {
	case !s.HasUpstream:
		return newPlan(s, "no upstream",
			Operation{Kind: OpRefuse, Detail: "no upstream configured — nothing to pull from"},
			Operation{Kind: OpTeach, Detail: "set a tracking branch first: git branch --set-upstream-to=<remote>/<branch>"},
		)
	case s.Ahead == 0 && s.Behind == 0:
		return newPlan(s, "up to date", Operation{Kind: OpNoop})
	case s.Behind > 0 && s.Ahead == 0:
		return newPlan(s, "fast-forward",
			Operation{Kind: OpFastForward, Detail: fmt.Sprintf("advance to %s (%d commit(s))", s.Dest.Ref(), s.Behind)},
		)
	case s.Ahead > 0 && s.Behind == 0:
		return newPlan(s, "nothing to pull",
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("you are %d commit(s) ahead of %s — nothing to pull", s.Ahead, s.Dest.Ref())},
		)
	default: // diverged: rebase local onto the remote
		return newPlan(s, "rebase onto remote",
			Operation{Kind: OpConfirm, Detail: fmt.Sprintf("rebase your %d local commit(s) onto %s — new commit IDs", s.Ahead, s.Dest.Ref())},
			Operation{Kind: OpReplay, Detail: s.Dest.Ref()},
		)
	}
}
