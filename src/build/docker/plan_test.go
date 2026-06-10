package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestApplyImageBuildStrategy pins the Load/Push disposition, in particular the
// invariant that under transport a single-platform image is NOT loaded into the
// daemon when the artifact is retained via the CAS layout (retainViaCAS) — the
// daemon copy would be a second, unconsumed retention. A non-CAS transport still
// loads (the daemon copy is then the retained artifact), and every non-transport
// path is unchanged.
func TestApplyImageBuildStrategy(t *testing.T) {
	single := []string{"linux/amd64"}
	multi := []string{"linux/amd64", "linux/arm64"}
	withReg := []build.RegistryTarget{{}}

	cases := []struct {
		name                           string
		transport, local, retainViaCAS bool
		platforms                      []string
		regs                           []build.RegistryTarget
		wantLoad, wantPush             bool
	}{
		{"transport + CAS retention, single-platform → no daemon copy",
			true, false, true, single, withReg, false, false},
		{"transport WITHOUT CAS retention, single-platform → daemon copy is the artifact",
			true, false, false, single, withReg, true, false},
		{"transport, multi-platform → never loads (export only)",
			true, false, true, multi, withReg, false, false},
		{"non-transport, single-platform → loads (caller pushes the tags)",
			false, false, false, single, withReg, true, false},
		{"non-transport, multi-platform → push directly",
			false, false, false, multi, withReg, false, true},
		{"--local → load, never push",
			false, true, false, single, withReg, true, false},
		{"no registries → build-only load",
			false, false, false, single, nil, true, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := &build.BuildPlan{Steps: []build.BuildStep{{
				Output:     build.OutputImage,
				Platforms:  c.platforms,
				Registries: c.regs,
				Tags:       []string{"example:1"},
			}}}
			applyImageBuildStrategy(plan, c.transport, c.local, c.retainViaCAS)
			got := plan.Steps[0]
			if got.Load != c.wantLoad || got.Push != c.wantPush {
				t.Errorf("Load=%v Push=%v, want Load=%v Push=%v", got.Load, got.Push, c.wantLoad, c.wantPush)
			}
		})
	}
}
