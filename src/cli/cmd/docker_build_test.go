package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

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

	if !hasRetention(plan) {
		t.Error("hasRetention() = false for load-only step with registries; want true")
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

	if hasRetention(plan) {
		t.Error("hasRetention() = true for step with no registries; want false")
	}
}
