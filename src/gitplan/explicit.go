package gitplan

import "fmt"

// ResolveExplicitPush plans an EXPLICIT cross-target push — `stagefreight push <remote> <branch>`,
// e.g. landing a feature branch onto origin/main. Ahead/behind are measured against the
// destination ref (not the branch's own upstream). A new or fast-forwardable destination is a
// direct push of HEAD to the ref (gated by Confirm when the destination is protected trunk); a
// destination that already contains you refuses; a diverged destination is handed back to git to
// rebase onto first — cross-branch replay execution is a follow-on (the linear-replay engine is
// coupled to the branch's own upstream).
func ResolveExplicitPush(s Situation) Plan {
	if s.InProgressOp != "" {
		return newPlan(s, "git operation in progress",
			Operation{Kind: OpRefuse, Detail: fmt.Sprintf("a git %s is in progress", s.InProgressOp)},
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("finish or abort it first (e.g. `git %s --abort`), then retry", s.InProgressOp)},
		)
	}
	ref := "HEAD:refs/heads/" + s.Dest.Branch
	push := Operation{Kind: OpDirectPush, Detail: ref}
	switch {
	case !s.HasUpstream:
		// Destination branch does not exist yet — create it from HEAD.
		return newPlan(s, "create "+s.Dest.Ref(),
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("%s does not exist yet — creating it from your HEAD", s.Dest.Ref())},
			push,
		)
	case s.Ahead == 0 && s.Behind == 0:
		return newPlan(s, "up to date", Operation{Kind: OpNoop})
	case s.Behind > 0 && s.Ahead == 0:
		return newPlan(s, "destination is ahead",
			Operation{Kind: OpRefuse, Detail: fmt.Sprintf("%s is %d commit(s) ahead of you", s.Dest.Ref(), s.Behind)},
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("nothing to push — your HEAD is already contained in %s", s.Dest.Ref())},
		)
	case s.Ahead > 0 && s.Behind == 0:
		// Fast-forward the destination to your HEAD. Landing on protected trunk is deliberate.
		if s.Dest.Protected {
			return newPlan(s, "fast-forward "+s.Dest.Ref(),
				Operation{Kind: OpConfirm, Detail: fmt.Sprintf("fast-forward protected %s to your HEAD (%d commit(s))", s.Dest.Ref(), s.Ahead)},
				push,
			)
		}
		return newPlan(s, "fast-forward "+s.Dest.Ref(), push)
	default: // diverged
		return newPlan(s, "diverged from destination",
			Operation{Kind: OpRefuse, Detail: fmt.Sprintf("your branch has diverged from %s (%d ahead, %d behind)", s.Dest.Ref(), s.Ahead, s.Behind)},
			Operation{Kind: OpTeach, Detail: fmt.Sprintf("rebase onto it first: git rebase %s/%s, then retry the push", s.Dest.Remote, s.Dest.Branch)},
		)
	}
}
