package render

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// Plan builds a forge-neutral Pipeline from StageFreight configuration.
//
// The pipeline graph is structurally stable across all lifecycle modes.
// Mode dispatch is the binary's job — the planner never branches the graph
// by lifecycle.mode. Review and publish exist for all modes; the runtime
// renders not_applicable where needed.
//
// Returns an error if ci.image is missing — no defaults, no guessing.
func Plan(cfg *config.Config) (model.Pipeline, error) {
	if cfg.CI.Image == "" {
		return model.Pipeline{}, fmt.Errorf("ci.image is required — set the StageFreight container image in .stagefreight.yml")
	}

	return model.Pipeline{
		Defaults: model.PipelineDefaults{
			Image:            cfg.CI.Image,
			Interruptible:    true,
			CancelSuperseded: true,
			CIContext:        true,
		},
		Jobs: []model.Job{
			{
				Name:     "audition",
				Stage:    "audition",
				Commands: []string{"stagefreight ci run audition"},
				Source:   model.SourceSpec{FullClone: true},
				Artifacts: model.ArtifactSpec{
					Paths:    []string{".stagefreight/"},
					ExpireIn: "1 week",
				},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:     "perform",
				Stage:    "perform",
				Commands: []string{"stagefreight ci run perform"},
				Source:   model.SourceSpec{FullClone: true},
				Artifacts: model.ArtifactSpec{
					Paths:    []string{".stagefreight/"},
					ExpireIn: "2 hours",
				},
				Capabilities: model.CapabilitySpec{Docker: true, OIDC: true},
				Routing:      model.RoutingSpec{Labels: cfg.CI.Routing.Perform.Labels},
			},
			{
				Name:     "review",
				Stage:    "review",
				Needs:    []string{"perform"},
				Commands: []string{"stagefreight ci run review"},
				Artifacts: model.ArtifactSpec{
					Paths:    []string{".stagefreight/security/"},
					ExpireIn: "1 week",
				},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:     "publish",
				Stage:    "publish",
				Needs:    []string{"perform", "review"},
				Commands: []string{"stagefreight ci run publish"},
				Source:   model.SourceSpec{FullClone: true},
				Policy:   model.PolicySpec{AllowFailure: true},
			},
			{
				Name:     "narrate",
				Stage:    "narrate",
				Needs:    []string{"perform", "publish"},
				Commands: []string{"stagefreight ci run narrate"},
				Source:   model.SourceSpec{FullClone: true},
				Policy:   model.PolicySpec{AllowFailure: true, WhenAlways: true},
			},
		},
	}, nil
}
