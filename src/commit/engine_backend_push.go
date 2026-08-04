package commit

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/go-git/go-git/v5/plumbing"
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
	eng := NewEngine(session, EngineOptions{OnEvent: g.onSyncEvent})

	// Explicit refspec (CI detached-HEAD): push HEAD straight to the ref. On a
	// non-fast-forward refusal, RebaseOnDiverge is honored via OBJECT-LAYER
	// replay (replayCommitOntoTip) — a tag pipeline's checkout always sits
	// behind the default branch (the dev pipeline's own scribe commit), so
	// divergence here is the normal case, not a race.
	if opts.Refspec != "" {
		res, err := eng.Execute(gitplan.DirectPush(remote, opts.Refspec), ExecuteOptions{Approved: true})
		if err == nil {
			r := syncResultFromOps(res.Performed, remote)
			r.PushedRef = opts.Refspec
			return r, nil
		}
		if !opts.RebaseOnDiverge || !isNonFastForwardErr(err) {
			return nil, err
		}
		return g.replayRefspecPush(session, remote, opts.Refspec, err)
	}

	if err := session.Fetch(remote); err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	policy := gitplan.DefaultPolicy()
	plan := gitplan.Resolve(gitplan.SituationFromStateConverge(session.State(), policy))
	res, err := eng.Execute(plan, ExecuteOptions{Approved: true})
	if err != nil {
		return nil, err
	}
	return syncResultFromOps(res.Performed, remote), nil
}

// replayPushRef is the transient local ref naming the replayed commit for the
// push refspec — both transports accept name-based refspecs; a raw sha source
// would not survive the embedded transport.
const replayPushRef = "refs/stagefreight/replay-push"

// replayPushAttempts bounds the fetch→replay→push loop. Each iteration replays
// the ORIGINAL commit onto the freshly observed tip, so attempts are idempotent;
// exhaustion degrades to the caller's existing non-FF handling (warn, continue).
const replayPushAttempts = 3

// replayRefspecPush recovers a refused detached-HEAD push by rebuilding the
// commit onto the remote's current tip at the object layer and pushing the
// result. Only branch destinations are replayable; anything else returns the
// original push error untouched.
func (g *GitBackend) replayRefspecPush(session *gitstate.SyncSession, remote, refspec string, pushErr error) (*SyncResult, error) {
	src, dst, ok := strings.Cut(strings.TrimPrefix(refspec, "+"), ":")
	if !ok || !strings.HasPrefix(dst, "refs/heads/") {
		return nil, pushErr
	}
	branch := strings.TrimPrefix(dst, "refs/heads/")

	repo := session.Repo()
	localRev, err := repo.ResolveRevision(plumbing.Revision(src))
	if err != nil {
		return nil, fmt.Errorf("replay: resolving %s: %w", src, err)
	}

	actions := []SyncAction{}
	for attempt := 1; attempt <= replayPushAttempts; attempt++ {
		if err := session.Fetch(remote); err != nil {
			return nil, fmt.Errorf("replay fetch: %w", err)
		}
		actions = append(actions, SyncFetch)
		tipRef, err := repo.Reference(plumbing.NewRemoteReferenceName(remote, branch), true)
		if err != nil {
			return nil, fmt.Errorf("replay: remote tip %s/%s: %w", remote, branch, err)
		}

		newHash, err := replayCommitOntoTip(repo, *localRev, tipRef.Hash())
		if err != nil {
			return nil, fmt.Errorf("replay onto %s: %w", short(tipRef.Hash()), err)
		}
		if g.OnCommitLine != nil {
			g.OnCommitLine("sync", fmt.Sprintf("replayed %s onto %s (attempt %d/%d)",
				short(*localRev), short(tipRef.Hash()), attempt, replayPushAttempts))
		}

		if err := repo.Storer.SetReference(plumbing.NewHashReference(replayPushRef, newHash)); err != nil {
			return nil, fmt.Errorf("replay: staging push ref: %w", err)
		}
		pushErr = session.Push(remote, replayPushRef+":"+dst, false)
		_ = repo.Storer.RemoveReference(plumbing.ReferenceName(replayPushRef))
		if pushErr == nil {
			actions = append(actions, SyncRebase, SyncPush)
			return &SyncResult{ActionsExecuted: actions, PushedRef: refspec}, nil
		}
		if !isNonFastForwardErr(pushErr) {
			return nil, fmt.Errorf("replay push: %w", pushErr)
		}
	}
	return nil, fmt.Errorf("replay push: upstream kept advancing through %d attempts: %w", replayPushAttempts, pushErr)
}

// isNonFastForwardErr classifies a push refusal caused by the remote ref having
// advanced — the embedded transport says "non-fast-forward update", system git
// says "non-fast-forward" or "fetch first". Anything else (auth, network,
// protected-branch policy) is not recoverable by replay.
func isNonFastForwardErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first")
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
		case gitplan.OpUpload, gitplan.OpDirectPush:
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
