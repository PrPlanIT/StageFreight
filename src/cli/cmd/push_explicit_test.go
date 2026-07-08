package cmd

import (
	"bytes"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// `push <remote> <branch>` lands a feature branch onto a (protected) trunk by fast-forward:
// ahead/behind are computed against the DESTINATION (origin/master), not the branch's own
// upstream. master is protected by DefaultPolicy, so the plan is Confirm-gated.
func TestPushExplicit_FeatureFastForwardsTrunk(t *testing.T) {
	remote, local, _, commitFile := pushScratch(t)

	lr, _ := git.PlainOpen(local)
	wt, _ := lr.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	commitFile(local, "f", "f\n", "feature commit")
	featHead, _ := lr.Head()

	var buf bytes.Buffer
	err := runPlanned(local, "origin", true, &buf, func(e *commit.Engine, p gitplan.Policy) (gitplan.Plan, error) {
		return e.PlanTo("origin", "master", p)
	})
	if err != nil {
		t.Fatalf("explicit push: %v (out: %s)", err, buf.String())
	}

	rr, _ := git.PlainOpen(remote)
	rref, err := rr.Reference(plumbing.NewBranchReferenceName("master"), true)
	if err != nil {
		t.Fatalf("remote master: %v", err)
	}
	if rref.Hash() != featHead.Hash() {
		t.Fatal("explicit push did not fast-forward origin/master to the feature HEAD")
	}
}

// Without --yes, the protected-trunk fast-forward is gated and mutates nothing.
func TestPushExplicit_ProtectedTrunkGated(t *testing.T) {
	remote, local, _, commitFile := pushScratch(t)
	before := remoteMasterHash(t, remote)

	lr, _ := git.PlainOpen(local)
	wt, _ := lr.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	commitFile(local, "f", "f\n", "feature commit")

	var buf bytes.Buffer
	err := runPlanned(local, "origin", false, &buf, func(e *commit.Engine, p gitplan.Policy) (gitplan.Plan, error) {
		return e.PlanTo("origin", "master", p)
	})
	if err == nil {
		t.Fatal("expected a confirmation-required error without --yes")
	}
	if remoteMasterHash(t, remote) != before {
		t.Fatal("protected trunk was mutated without approval")
	}
}
