package cmd

import (
	"bytes"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// `sf pull` on a behind branch fast-forwards local to the remote.
func TestPull_BehindFastForwards(t *testing.T) {
	remote, local, other, commitFile := pushScratch(t)
	commitFile(other, "b", "b\n", "other b")
	otherR, _ := git.PlainOpen(other)
	if err := otherR.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("push b: %v", err)
	}

	var buf bytes.Buffer
	err := runPlanned(local, "origin", false, &buf, func(e *commit.Engine, p gitplan.Policy) gitplan.Plan {
		return e.PlanPull(p)
	})
	if err != nil {
		t.Fatalf("pull: %v (out: %s)", err, buf.String())
	}

	lr, _ := git.PlainOpen(local)
	lh, _ := lr.Head()
	rr, _ := git.PlainOpen(remote)
	rref, _ := rr.Reference(plumbing.NewBranchReferenceName("master"), true)
	if lh.Hash() != rref.Hash() {
		t.Fatal("pull did not fast-forward local to the remote")
	}
}
