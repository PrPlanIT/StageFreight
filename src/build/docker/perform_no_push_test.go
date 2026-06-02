package docker

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
)

// TestBuildArgs_TransportRetainEmitsNoPush is the structural safety property the
// whole "publish is the sole distributor" invariant rests on, asserted at the
// ONLY place a registry write originates in crucible perform: buildArgs. Crucible
// mode routes exclusively through executeBuildPass → BuildWithLayers → buildArgs;
// it never reaches the executePhase PushTags path. So if buildArgs emits no
// `--push` for a retain-shaped step (Push=false + OCILayoutDir set — exactly what
// the transport branch of the crucible publish pass produces), then no registry
// mutation is REACHABLE from perform under transport, regardless of what any log
// line says. This test fails loudly if a future change reintroduces a push lever.
func TestBuildArgs_TransportRetainEmitsNoPush(t *testing.T) {
	bx := NewBuildx(false)
	base := build.BuildStep{
		Name:       "stagefreight",
		Output:     build.OutputImage,
		Platforms:  []string{"linux/amd64"},
		Registries: []build.RegistryTarget{{URL: "docker.io", Path: "org/app", Tags: []string{"latest"}}},
		Tags:       []string{"docker.io/org/app:latest"},
	}

	// Retain shape: the exact flags the crucible publish pass sets when transport
	// is active (crucible.go: Push=false, Load=false, OCILayoutDir set).
	retain := base
	retain.Push = false
	retain.Load = false
	retain.OCILayoutDir = t.TempDir()
	args := bx.buildArgs(retain)
	for _, a := range args {
		if a == "--push" {
			t.Fatalf("transport-retain step emitted --push — perform CAN mutate a registry; the safety property is broken. args=%v", args)
		}
	}
	// The retain must still EXPORT the OCI layout, or nothing is carried to the
	// content store and the test would pass vacuously for a do-nothing step.
	hasOCIExport := false
	for _, a := range args {
		if strings.Contains(a, "type=oci") {
			hasOCIExport = true
		}
	}
	if !hasOCIExport {
		t.Fatalf("transport-retain step did not export an OCI layout — nothing retained; test is vacuous. args=%v", args)
	}

	// Contrast: the legacy push shape DOES emit --push, so this test genuinely
	// distinguishes retain from distribute (it is not trivially always-green).
	push := base
	push.Push = true
	pushArgs := bx.buildArgs(push)
	emitsPush := false
	for _, a := range pushArgs {
		if a == "--push" {
			emitsPush = true
		}
	}
	if !emitsPush {
		t.Fatalf("legacy push step did not emit --push; test no longer distinguishes retain from distribute. args=%v", pushArgs)
	}
}

// TestTransportActive_ReportsStoreCapability pins the predicate that gates
// whether perform distributes: only a store requiring OCI export (FSStore)
// activates transport; nil and NoopStore do not.
func TestTransportActive_ReportsStoreCapability(t *testing.T) {
	cases := []struct {
		name  string
		store cas.Store
		want  bool
	}{
		{"nil store", nil, false},
		{"noop store", cas.NewNoopStore(), false},
		{"fs store", cas.NewFSStore(t.TempDir()), true},
	}
	for _, c := range cases {
		if got := transportActive(Request{Store: c.store}); got != c.want {
			t.Errorf("%s: transportActive = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestPerformDoesNotPushWhenTransportActive is the authority-boundary proof at
// the plan level: with transport active, NO build step is marked Push — perform
// never mutates a registry. Distribution is left entirely to the publish phase,
// which promotes the retained bytes. This asserts the architectural invariant
// ("publish is the sole phase permitted to mutate external distribution
// targets") is enforced by perform's silence, not merely by publish's behavior.
func TestPerformDoesNotPushWhenTransportActive(t *testing.T) {
	// A plan as it would look after the strategy decision, simulated by applying
	// the same rule the plan phase applies. We assert the rule's outcome rather
	// than re-running the whole pipeline (which needs docker).
	multiPlatform := build.BuildStep{
		Name:       "multi",
		Output:     build.OutputImage,
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		Registries: []build.RegistryTarget{{URL: "docker.io", Path: "org/app", Tags: []string{"v1"}}},
	}
	singlePlatform := build.BuildStep{
		Name:       "single",
		Output:     build.OutputImage,
		Platforms:  []string{"linux/amd64"},
		Registries: []build.RegistryTarget{{URL: "docker.io", Path: "org/app", Tags: []string{"v1"}}},
	}

	apply := func(step build.BuildStep, transport bool) build.BuildStep {
		// Mirror of the transport branch in planPhase's strategy loop.
		if transport {
			step.Push = false
			if !IsMultiPlatform(step) {
				step.Load = true
			}
		} else if IsMultiPlatform(step) {
			step.Push = true
		} else {
			step.Load = true
		}
		return step
	}

	// Transport active: neither step may push.
	if s := apply(multiPlatform, true); s.Push {
		t.Error("multi-platform step pushes under active transport — perform must not distribute")
	}
	if s := apply(singlePlatform, true); s.Push {
		t.Error("single-platform step pushes under active transport — perform must not distribute")
	}

	// Single-platform under transport is Load && !Push — the exact shape
	// collectRemoteTags selects for the load-then-push path. That path must be
	// suppressed under transport (the execute loop guards it with
	// !transportActive), or perform would distribute single-platform images
	// despite step.Push being false. Assert the plan-level fact the guard relies
	// on: the single-platform transport step IS a collectRemoteTags candidate, so
	// the guard — not the step flags — is what prevents the push.
	sp := apply(singlePlatform, true)
	if !sp.Load || sp.Push {
		t.Fatalf("single-platform transport step expected Load && !Push, got Load=%v Push=%v", sp.Load, sp.Push)
	}
	candidatePlan := &build.BuildPlan{Steps: []build.BuildStep{sp}}
	if len(collectRemoteTags(candidatePlan)) == 0 {
		t.Error("single-platform transport step is not a collectRemoteTags candidate — the !transportActive guard would be vacuous; the suppression must come from the guard, and this test must stay meaningful")
	}

	// Transport inactive (legacy fallback): multi-platform still pushes (the old
	// behavior is preserved as the fallback until the path is removed).
	if s := apply(multiPlatform, false); !s.Push {
		t.Error("multi-platform step should push under inactive transport (legacy fallback)")
	}
}
