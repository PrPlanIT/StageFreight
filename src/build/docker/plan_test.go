package docker

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestApplyImageBuildStrategy pins the Load/Push disposition, in particular the
// invariant that under transport a single-platform image is NOT loaded into the
// daemon when the artifact is retained via the CAS layout (retainViaCAS) — the
// daemon copy would be a second, unconsumed retention. A non-CAS transport still
// loads (the daemon copy is then the retained artifact). Without transport, a
// step with registries pushes in-build at any platform count — nothing pushes a
// loaded copy afterwards.
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
		{"non-transport, single-platform → push directly",
			false, false, false, single, withReg, false, true},
		{"non-transport, multi-platform → push directly",
			false, false, false, multi, withReg, false, true},
		{"--local → load, never push",
			false, true, false, single, withReg, true, false},
		{"no registries, no transport → build-only load",
			false, false, false, single, nil, true, false},
		{"no registries under transport + CAS → layout only (merge request, feature branch)",
			true, false, true, single, nil, false, false},
		{"no registries under transport without CAS → daemon copy is the artifact",
			true, false, false, single, nil, true, false},
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

// TestDaemonLoadSteps pins which strategies a daemonless runner (remote
// buildkitd, no Docker) can run: transport with content-store retention and
// multi-platform builds never --load, so they need no daemon; local builds,
// registry-less builds and transport without the content store do, and are
// named so the build fails up front instead of mid-build.
func TestDaemonLoadSteps(t *testing.T) {
	single := []string{"linux/amd64"}
	multi := []string{"linux/amd64", "linux/arm64"}
	withReg := []build.RegistryTarget{{}}

	cases := []struct {
		name                           string
		transport, local, retainViaCAS bool
		platforms                      []string
		regs                           []build.RegistryTarget
		wantLoads                      int
	}{
		{"daemonless: transport + CAS, single-platform", true, false, true, single, withReg, 0},
		{"daemonless: transport, multi-platform", true, false, true, multi, withReg, 0},
		{"daemonless: non-transport, multi-platform pushes", false, false, false, multi, withReg, 0},
		{"daemonless: non-transport, single-platform pushes", false, false, false, single, withReg, 0},
		{"needs daemon: transport without CAS", true, false, false, single, withReg, 1},
		{"needs daemon: --local", false, true, false, single, withReg, 1},
		{"needs daemon: no registries, no transport", false, false, false, single, nil, 1},
		{"daemonless: no registries under transport + CAS", true, false, true, single, nil, 0},
		{"needs daemon: no registries under transport without CAS", true, false, false, single, nil, 1},
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
			got := daemonLoadSteps(plan)
			if len(got) != c.wantLoads {
				t.Fatalf("daemonLoadSteps = %v, want %d step(s)", got, c.wantLoads)
			}
			if c.wantLoads > 0 && got[0] != "example:1" {
				t.Errorf("daemonLoadSteps named %q, want the step's tag", got[0])
			}
		})
	}
}

// Local cache exports on every strategy; registry cache only from a pushing step.
func TestApplyImageBuildStrategy_CacheExports(t *testing.T) {
	local := build.CacheRef{Type: "local", Ref: "/stagefreight/cache/buildkit/x", Direction: "export"}
	reg := build.CacheRef{Type: "registry", Ref: "cr.example/cache:x", Direction: "export"}
	cases := []struct {
		name                           string
		transport, local, retainViaCAS bool
		wantTypes                      []string
	}{
		{"transport + CAS (k8s or docker runner): local only", true, false, true, []string{"local"}},
		{"in-build push: local and registry", false, false, false, []string{"local", "registry"}},
		{"--local load: local only", false, true, false, []string{"local"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := &build.BuildPlan{Steps: []build.BuildStep{{
				Output: build.OutputImage, Platforms: []string{"linux/amd64"},
				Registries: []build.RegistryTarget{{}}, Tags: []string{"example:1"},
				CacheTo: []build.CacheRef{local, reg},
			}}}
			applyImageBuildStrategy(plan, c.transport, c.local, c.retainViaCAS)
			var got []string
			for _, r := range plan.Steps[0].CacheTo {
				got = append(got, r.Type)
			}
			if strings.Join(got, ",") != strings.Join(c.wantTypes, ",") {
				t.Fatalf("cache exports = %v, want %v", got, c.wantTypes)
			}
		})
	}
}
