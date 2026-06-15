package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
)

// TestAcceptedState locks the reconcile gate's fail-closed contract: reconcile
// only when provably on the default branch. The event string is deliberately
// untrusted (it varies by forge), so these cases assert behavior from
// branch/default-branch alone.
func TestAcceptedState(t *testing.T) {
	cases := []struct {
		name string
		ctx  ci.CIContext
		want bool
	}{
		{"default branch push", ci.CIContext{Event: "push", Branch: "main", DefaultBranch: "main"}, true},
		{"non-main default branch", ci.CIContext{Event: "push", Branch: "master", DefaultBranch: "master"}, true},
		{"feature branch", ci.CIContext{Event: "push", Branch: "renovate/foo", DefaultBranch: "main"}, false},
		{"gitlab MR pipeline (empty branch)", ci.CIContext{Event: "merge_request_event", Branch: "", DefaultBranch: "main"}, false},
		{"github PR (source branch)", ci.CIContext{Event: "pull_request", Branch: "renovate/bar", DefaultBranch: "main"}, false},
		{"tag build (empty branch)", ci.CIContext{Event: "push", Branch: "", Tag: "v1.2.3", DefaultBranch: "main"}, false},
		{"unknown default branch", ci.CIContext{Event: "push", Branch: "main", DefaultBranch: ""}, false},
		{"empty context", ci.CIContext{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := c.ctx
			if got := acceptedState(&ctx); got != c.want {
				t.Errorf("acceptedState(%+v) = %v, want %v", c.ctx, got, c.want)
			}
		})
	}
}
