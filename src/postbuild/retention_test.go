package postbuild

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestRetentionHook_SkipsWhenNoBuildPlan(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	// pc.BuildPlan is nil by default
	if hook.Condition(pc) {
		t.Error("Condition() = true when BuildPlan is nil; want false")
	}
}

func TestRetentionHook_SkipsWhenNoActiveRetention(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	pc.BuildPlan = &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "docker.io",
						Path:     "prplanit/example",
						Provider: "docker",
						// Retention not configured — KeepLast == 0
					},
				},
			},
		},
	}
	if hook.Condition(pc) {
		t.Error("Condition() = true when no active retention policies; want false")
	}
}

func TestRetentionHook_ConditionTrueWhenActiveRetention(t *testing.T) {
	hook := RetentionHook()
	pc := makePC()
	pc.BuildPlan = &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "docker.io",
						Path:     "prplanit/example",
						Provider: "docker",
						Retention: config.RetentionPolicy{
							KeepLast: 5,
						},
					},
				},
			},
		},
	}
	if !hook.Condition(pc) {
		t.Error("Condition() = false when BuildPlan has active retention; want true")
	}
}

func TestHasRetention_LoadOnlyWithRegistries(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load: true,
				Push: false,
				Registries: []build.RegistryTarget{
					{
						URL:      "docker.io",
						Path:     "prplanit/example",
						Provider: "docker",
						Retention: config.RetentionPolicy{
							KeepLast: 5,
						},
					},
				},
			},
		},
	}

	if !HasRetention(plan) {
		t.Error("HasRetention() = false for load-only step with registries; want true")
	}
}

func TestHasRetention_NoRegistries(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Load:       true,
				Push:       false,
				Registries: nil,
			},
		},
	}

	if HasRetention(plan) {
		t.Error("HasRetention() = true for step with no registries; want false")
	}
}
