package cmd

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// Committing a satellite's managed config to its forge is forge mutation, so the
// governance APPLY runs in publishPhaseRunner (which owns forge mutation), while
// performPhaseRunner only PREVIEWS the plan — perform calls governanceReconcile(...,false),
// publish calls governanceReconcile(...,true). These lock the two guards that keep that
// split safe: a non-governance repo's publish never touches the reconcile path, and a
// governance repo whose source can't be resolved skips cleanly instead of failing the job.
func TestGovernanceReconcile_NoProfilesIsNoop(t *testing.T) {
	cfg := &config.Config{} // no governance.profiles
	ciCtx := &ci.CIContext{Branch: "main", DefaultBranch: "main"}

	// Both the perform (apply=false) and publish (apply=true) call sites must be a
	// pure no-op when the repo declares no governance catalog — otherwise every
	// ordinary image repo's publish would try to reconcile.
	if err := governanceReconcile(context.Background(), cfg, ciCtx, ci.RunOptions{}, false); err != nil {
		t.Fatalf("no profiles, plan: want nil no-op, got %v", err)
	}
	if err := governanceReconcile(context.Background(), cfg, ciCtx, ci.RunOptions{}, true); err != nil {
		t.Fatalf("no profiles, apply: want nil no-op, got %v", err)
	}
}

func TestGovernanceReconcile_ProfilesButNoSourceSkips(t *testing.T) {
	cfg := &config.Config{}
	cfg.Governance.Profiles = config.OrderedGovProfiles{{ID: "p1"}}
	// No sources.primary URL and a non-CI context → the source cannot be resolved.
	// The reconcile must render a skip and return nil, not error out the publish job.
	ciCtx := &ci.CIContext{Branch: "main", DefaultBranch: "main"}

	if err := governanceReconcile(context.Background(), cfg, ciCtx, ci.RunOptions{}, true); err != nil {
		t.Fatalf("profiles but unresolvable source: want nil skip, got %v", err)
	}
}
