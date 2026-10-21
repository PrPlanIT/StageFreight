package cmd

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// A pipeline with nothing to scan records security not_applicable without needing a Docker
// daemon, and publish is then authorized — the docs-repo case (no builds configured).
func TestSecurityRunner_NothingToScanSkipsAndAuthorizesPublish(t *testing.T) {
	cases := []struct {
		name  string
		build cistate.SubsystemState
	}{
		{"no builds configured", cistate.SubsystemState{
			Name: "build", Attempted: true, Outcome: "not_applicable", Reason: "no builds configured",
		}},
		{"build produced no images", cistate.SubsystemState{
			Name: "build", Attempted: true, Outcome: "success",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seedSubsystems(t, dir, tc.build)
			ciCtx := &ci.CIContext{Provider: "gitlab", Workspace: dir, Branch: "main"}
			appCfg := &config.Config{}
			appCfg.Security.Enabled = true

			if err := securityRunner(context.Background(), appCfg, ciCtx, ci.RunOptions{}); err != nil {
				t.Fatalf("securityRunner: %v", err)
			}
			st, err := cistate.ReadState(dir)
			if err != nil {
				t.Fatalf("read state: %v", err)
			}
			sec := st.GetSubsystem("security")
			if sec == nil || sec.Outcome != "not_applicable" {
				t.Fatalf("security outcome = %+v, want not_applicable", sec)
			}
			if err := authorizePhase(ciCtx, dir, "publish"); err != nil {
				t.Fatalf("publish should be authorized: %v", err)
			}
		})
	}
}
