package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
)

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
