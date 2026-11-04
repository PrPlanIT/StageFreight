package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/workflow"
)

// withTrunkWorkflow declares the trunk model for the test: main lands through merge
// requests, feature branches are pushed to directly.
func withTrunkWorkflow(t *testing.T) {
	t.Helper()
	prev := cfg
	t.Cleanup(func() { cfg = prev })
	cfg = &config.Config{
		Git: config.GitConfig{Branches: map[string]string{"main": "^main$", "feature": "^feature/"}},
		Workflow: config.WorkflowConfig{
			Branches: config.OrderedBranchClasses{
				{ID: "main", Match: "main", Landing: config.LandingMergeRequest},
				{ID: "feature", Match: "feature", Lifetime: config.LifetimeShort, Landing: config.LandingDirect},
			},
			Promote: config.OrderedPromotions{{ID: "land", From: "feature", To: "main", Method: config.PromoteMergeRequest}},
		},
	}
	t.Setenv("SF_CI_PROVIDER", "")
	prevRole := workflowRole
	t.Cleanup(func() { workflowRole = prevRole })
	workflowRole = ""
}

func remoteHead(t *testing.T, remote, branch string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(remote)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return plumbing.ZeroHash
	}
	return ref.Hash()
}

// A direct push to a merge-request class is refused before anything is sent, explaining the
// way through; an override pushes and says so.
func TestWorkflowGate_PushToMergeRequestClass(t *testing.T) {
	withTrunkWorkflow(t)
	local, remote := divergedRemote(t)
	before := remoteHead(t, remote, "main")

	err := refspecPush(local, "origin", "HEAD:main", true, false)
	if err == nil || !strings.Contains(err.Error(), "through a merge request") || !strings.Contains(err.Error(), "--maintainer-override") {
		t.Fatalf("push to main must be refused with the rule and the override named, got %v", err)
	}
	if remoteHead(t, remote, "main") != before {
		t.Fatal("a refused push must not reach the remote")
	}

	// Overriding is a producer's call.
	err = refspecPush(local, "origin", "HEAD:refs/heads/main", true, true)
	if err == nil || !strings.Contains(err.Error(), "needs the producer role") {
		t.Fatalf("a talent's override must be refused, got %v", err)
	}
	workflowRole = "producer" // no forge here, so the ceiling is unknown and --role unbounded
	if err := refspecPush(local, "origin", "HEAD:refs/heads/main", true, true); err != nil {
		t.Fatalf("a producer's override must go through: %v", err)
	}
	if remoteHead(t, remote, "main") == before {
		t.Error("the overridden push did not land")
	}
}

// Branches the workflow lets you push to directly are untouched by the gate.
func TestWorkflowGate_PushToDirectClass(t *testing.T) {
	withTrunkWorkflow(t)
	local, remote := divergedRemote(t)
	if err := refspecPush(local, "origin", "HEAD:refs/heads/feature/x", false, false); err != nil {
		t.Fatalf("push to a feature branch: %v", err)
	}
	if remoteHead(t, remote, "feature/x") == plumbing.ZeroHash {
		t.Error("feature branch not created")
	}
}

// In CI the forge enforces the workflow; the local gate stands aside.
func TestWorkflowGate_StandsAsideInCI(t *testing.T) {
	withTrunkWorkflow(t)
	t.Setenv("SF_CI_PROVIDER", "gitlab")
	local, _ := divergedRemote(t)
	if err := refspecPush(local, "origin", "HEAD:refs/heads/main", true, false); err != nil {
		t.Fatalf("CI push: %v", err)
	}
}

// A commit made on a merge-request class is a governance block, which --maintainer-override
// bypasses and the commit result records.
func TestWorkflowGate_CommitOnMergeRequestClass(t *testing.T) {
	withTrunkWorkflow(t)
	local, _ := divergedRemote(t) // checked out on main

	var out bytes.Buffer
	blocks := workflowBlocks(&out, commitLandings(local, &commit.Plan{}, workflow.Standing{Role: workflow.Talent})...)
	if len(blocks) != 1 || blocks[0].ID != "workflow.landing" || !blocks[0].Overrideable() {
		t.Fatalf("commit on main must be one overrideable workflow block, got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Message, "feature branch (matching ^feature/)") {
		t.Errorf("block must teach the way through: %s", blocks[0].Message)
	}

	// Pushing elsewhere is checked too: commit on main, pushed to a feature branch, is
	// still a commit on main; pushed to main it is not counted twice.
	plan := &commit.Plan{Push: commit.PushOptions{Enabled: true, Refspec: "HEAD:main"}}
	if blocks := workflowBlocks(&out, commitLandings(local, plan, workflow.Standing{Role: workflow.Talent})...); len(blocks) != 1 {
		t.Errorf("commit and push to the same branch is one landing, got %d", len(blocks))
	}
}

// Evaluate mode reports and lets the action through.
func TestWorkflowGate_Evaluate(t *testing.T) {
	withTrunkWorkflow(t)
	cfg.Workflow.Branches[0].Enforcement = config.EnforcementEvaluate
	local, _ := divergedRemote(t)
	var out bytes.Buffer
	if blocks := workflowBlocks(&out, commitLandings(local, &commit.Plan{}, workflow.Standing{Role: workflow.Talent})...); len(blocks) != 0 {
		t.Errorf("evaluate must not block, got %+v", blocks)
	}
	if !strings.Contains(out.String(), "evaluate, not enforced") {
		t.Errorf("evaluate must report the finding, got %q", out.String())
	}
}

// A release the repository can't be checked for is refused, not waved through.
func TestWorkflowGate_TagUncheckable(t *testing.T) {
	withTrunkWorkflow(t)
	cfg.Workflow.Release = config.WorkflowRelease{From: map[string][]string{"main": {"minor"}}}
	f := tagRelease(t.TempDir(), "v0.16.0", "0000000000000000000000000000000000000000")
	if f == nil || f.Evaluate || !strings.Contains(f.Message, "can't check") {
		t.Errorf("an unreadable repository must refuse, got %+v", f)
	}
}

// A long-lived branch that takes direct pushes is shared state a director moves.
func TestWorkflowGate_LongLivedDirectNeedsDirector(t *testing.T) {
	withTrunkWorkflow(t)
	cfg.Git.Branches["stable"] = "^stable$"
	cfg.Workflow.Branches = append(cfg.Workflow.Branches, config.BranchClass{ID: "stable", Match: "stable", Landing: config.LandingDirect})
	local, remote := divergedRemote(t)
	err := refspecPush(local, "origin", "HEAD:refs/heads/stable", false, false)
	if err == nil || !strings.Contains(err.Error(), "needs the director role") || !strings.Contains(err.Error(), "--role director") {
		t.Fatalf("a talent's push to a long-lived branch must be refused, got %v", err)
	}
	workflowRole = "director"
	if err := refspecPush(local, "origin", "HEAD:refs/heads/stable", false, false); err != nil {
		t.Fatalf("a director's push: %v", err)
	}
	if remoteHead(t, remote, "stable") == plumbing.ZeroHash {
		t.Error("stable not pushed")
	}
}
