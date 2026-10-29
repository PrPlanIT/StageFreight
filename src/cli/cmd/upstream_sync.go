package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// syncOut is the local ref a sync's result is pushed from; syncUpstreamCommit holds
// upstream's commit (an annotated tag peeled), which is what a branch may point at.
const (
	syncOut            = plumbing.ReferenceName("refs/stagefreight/sync/out")
	syncUpstreamCommit = plumbing.ReferenceName("refs/stagefreight/sync/upstream")
)

// syncUpstream is upstream.sync: on the default branch, it joins upstream's newest release
// (or branch head) into our branch with the configured strategy and pushes the result, which
// starts a fresh pipeline on it. pushed reports that our branch moved, so this run leaves the
// dependency update to that pipeline; refuse is set only by on_conflict: fail.
//
// Nothing is rewritten on a conflict. Our branch is pushed only if it still points at the
// commit this run started from, so a concurrent push is never overwritten; the next run
// syncs again.
func syncUpstream(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) (pushed bool, refuse string) {
	sy := appCfg.Upstream.Sync
	if sy.From == "" || !ciCtx.IsCI() || !acceptedState(ciCtx) || ciCtx.Tag != "" || ciCtx.SHA == "" {
		return false, ""
	}
	sources, err := upstream.Sources(appCfg.Upstream)
	if err != nil {
		return false, ""
	}
	var src upstream.Source
	for _, s := range sources {
		if s.ID == sy.From {
			src = s
		}
	}
	branch := ciCtx.DefaultBranch
	facts := map[string]string{}
	sub := cistate.SubsystemState{Name: "sync", Attempted: true, Completed: true, AllowFailure: true, Outcome: "success", Results: facts}
	record := func(outcome, reason string) {
		sub.Outcome, sub.Reason = outcome, reason
		fmt.Printf("  sync: %s\n", reason)
		recordSubsystemOutcome(rootDir, sub)
	}

	// What to sync onto: upstream's newest release, or a branch head.
	var ref plumbing.ReferenceName
	var label, want string
	if sy.Onto.Branch != "" {
		head, err := upstream.BranchHead(ctx, src, sy.Onto.Branch)
		if err != nil {
			record("warning", err.Error())
			return false, ""
		}
		ref, label, want = plumbing.NewBranchReferenceName(sy.Onto.Branch), sy.Onto.Branch, head
	} else {
		l := listUpstream(ctx, src)
		if l.err != nil || len(l.releases) == 0 {
			record("warning", fmt.Sprintf("%s: no release to sync onto (%v)", src, l.err))
			return false, ""
		}
		rel := l.releases[0]
		ref, label, want = plumbing.NewTagReferenceName(rel.Tag), rel.Tag, rel.Commit
	}
	facts["target"] = label

	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		record("warning", err.Error())
		return false, ""
	}
	if err := upstream.Fetch(ctx, repo, src, ref); err != nil {
		record("warning", err.Error())
		return false, ""
	}
	fetched, err := repo.Reference(upstream.FetchRef(src.ID), true)
	if err != nil {
		record("warning", err.Error())
		return false, ""
	}
	target := peel(repo, fetched.Hash())
	if want != "" && target.String() != want {
		record("warning", fmt.Sprintf("%s %s moved while fetching; next run", src, label))
		return false, ""
	}

	if err := repo.Storer.SetReference(plumbing.NewHashReference(syncUpstreamCommit, target)); err != nil {
		record("warning", err.Error())
		return false, ""
	}
	session, err := gitstate.OpenSyncSession(rootDir, appCfg)
	if err != nil {
		record("warning", err.Error())
		return false, ""
	}
	if sy.Shadow != "" {
		if err := session.Push("origin", "+"+syncUpstreamCommit.String()+":refs/heads/"+sy.Shadow, false); err != nil {
			fmt.Printf("  sync: shadow branch %s not updated: %v\n", sy.Shadow, err)
		}
	}

	head := plumbing.NewHash(ciCtx.SHA)
	msg := fmt.Sprintf("Merge upstream %s %s\n\nUpstream-Rev: %s\nUpdated-By: StageFreight\n", src.ID, label, target)
	plan, err := upstream.PlanSync(repo, head, target, upstream.SyncOptions{
		Strategy:     sy.EffectiveStrategy(),
		Policy:       upstream.FilePolicy(sy.Files),
		Sig:          commit.ResolveAuthorSignature(repo),
		MergeMessage: msg,
		HoldMessage:  fmt.Sprintf("Keep the fork's version of files upstream %s %s changed", src.ID, label),
	})
	if err != nil {
		record("warning", err.Error())
		return false, ""
	}
	facts["result"] = plan.Result
	if len(plan.Held) > 0 {
		// Upstream moved on in these; the fork keeps its version (take: fork). Said every
		// time, so a held file never silently misses an upstream fix.
		facts["held"] = strings.Join(plan.Held, ",")
		fmt.Printf("  sync: kept the fork's version of %s (upstream %s changed them)\n", strings.Join(plan.Held, ", "), label)
	}
	if len(plan.Overridden) > 0 {
		facts["overridden"] = strings.Join(plan.Overridden, ",")
		fmt.Printf("  sync: took upstream's version of %s over the fork's edits\n", strings.Join(plan.Overridden, ", "))
	}
	if len(plan.Dropped) > 0 {
		facts["dropped"] = strings.Join(plan.Dropped, ",")
		fmt.Printf("  sync: dropped %d fork commit(s) that became empty after sync: %s\n", len(plan.Dropped), strings.Join(plan.Dropped, "; "))
	}
	switch plan.Result {
	case upstream.SyncInSync:
		record("success", fmt.Sprintf("%s in sync with %s %s", branch, src.ID, label))
		return false, ""
	case upstream.SyncConflict:
		facts["conflicts"] = strings.Join(plan.Conflicts, ",")
		facts[cistate.SignalsKey] = config.SignalSyncConflict
		reason := fmt.Sprintf("%s %s does not join %s cleanly — %s", src.ID, label, branch, plan.Reason)
		if len(plan.Conflicts) > 0 {
			reason += ": " + strings.Join(plan.Conflicts, ", ")
		}
		switch sy.EffectiveOnConflict() {
		case config.SyncConflictFail:
			sub.AllowFailure = false
			record("failed", reason)
			return false, reason
		case config.SyncConflictMR:
			reason += "; " + openSyncMR(ctx, session, ciCtx, src, label, branch, syncUpstreamCommit, plan,
				"Upstream's changes overlap ours, so StageFreight changed nothing. This branch is upstream's "+label+"; resolve it against "+branch+" and merge.")
		}
		record("warning", reason)
		return false, ""
	}

	// Synced: push our moved branch, but only from the commit this run started on.
	if remote, err := session.RemoteRefHash("origin", branch); err != nil || remote != head {
		record("warning", fmt.Sprintf("%s moved since this run started; the next run syncs onto it", branch))
		return false, ""
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(syncOut, plan.NewHead)); err != nil {
		record("warning", err.Error())
		return false, ""
	}
	spec := syncOut.String() + ":refs/heads/" + branch
	if plan.Rewrites {
		spec = "+" + spec
	}
	if err := session.Push("origin", spec, false); err != nil {
		reason := fmt.Sprintf("%s onto %s %s not pushed: %v", branch, src.ID, label, err)
		if plan.Rewrites {
			reason += "; " + openSyncMR(ctx, session, ciCtx, src, label, branch, syncOut, plan,
				"This branch is "+branch+" rebased onto upstream "+label+". StageFreight could not rewrite "+branch+" (the forge refused the force push): allow StageFreight to force-push "+branch+", or replace "+branch+" with this branch. Merging it would duplicate our commits.")
		}
		facts[cistate.SignalsKey] = config.SignalSyncConflict
		record("warning", reason)
		return false, ""
	}
	record("success", fmt.Sprintf("%s synced onto %s %s (%s) → %s", branch, src.ID, label, sy.EffectiveStrategy(), plan.NewHead.String()[:9]))
	return true, ""
}

// peel resolves an annotated tag object to the commit it names; any other hash is itself.
func peel(repo *git.Repository, h plumbing.Hash) plumbing.Hash {
	if tag, err := repo.TagObject(h); err == nil {
		if c, err := tag.Commit(); err == nil {
			return c.Hash
		}
	}
	return h
}

var unsafeBranchChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// openSyncMR pushes from to a sync branch and opens a merge request from it into our branch
// for a human. It returns what happened, for the run's reason line.
func openSyncMR(ctx context.Context, session *gitstate.SyncSession, ciCtx *ci.CIContext, src upstream.Source, label, branch string, from plumbing.ReferenceName, plan upstream.SyncPlan, explanation string) string {
	mrBranch := "stagefreight/sync/" + src.ID + "/" + unsafeBranchChars.ReplaceAllString(label, "-")
	if err := session.Push("origin", "+"+from.String()+":refs/heads/"+mrBranch, false); err != nil {
		return fmt.Sprintf("merge request branch %s not pushed: %v", mrBranch, err)
	}
	desc := explanation
	if plan.Reason != "" {
		desc += "\n\n" + plan.Reason + "."
	}
	if len(plan.Conflicts) > 0 {
		desc += "\n\nConflicting paths:\n- " + strings.Join(plan.Conflicts, "\n- ")
	}
	fc, err := newForgeClient(forge.Provider(ciCtx.Provider), ciCtx.RepoURL)
	if err != nil {
		return fmt.Sprintf("branch %s pushed; merge request not opened: %v", mrBranch, err)
	}
	mr, err := fc.CreateMR(ctx, forge.MROptions{
		Title:        fmt.Sprintf("Sync upstream %s %s into %s", src.ID, label, branch),
		Description:  desc,
		SourceBranch: mrBranch,
		TargetBranch: branch,
	})
	if err != nil {
		return fmt.Sprintf("branch %s pushed; merge request not opened (one may already exist): %v", mrBranch, err)
	}
	return "merge request " + mr.URL
}
