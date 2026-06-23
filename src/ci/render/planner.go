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
					// perform's .stagefreight/ carries the content store
					// (objects/ = the built OCI layouts) that publish resolves and
					// promotes. The transport guarantee depends on these bytes
					// surviving until publish runs — which may be gated/queued/
					// retried hours after perform. 2h was tuned for manifests only
					// and is too tight once the layout rides along; 1 day covers a
					// realistic perform→publish gap including a manual publish gate.
					Paths:    []string{".stagefreight/"},
					ExpireIn: "1 day",
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
					// pipeline.json carries review's recorded security outcome
					// forward to publish, whose authorization gate reads it. Without
					// it, publish sees only perform's stale state and the gate is
					// blind. security/ holds the scan reports themselves. (review
					// deliberately does NOT re-forward the whole .stagefreight/ —
					// that would drag perform's content store along with it.)
					Paths:    []string{".stagefreight/security/", ".stagefreight/pipeline.json"},
					ExpireIn: "1 week",
				},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:         "publish",
				Stage:        "publish",
				Needs:        []string{"perform", "review"},
				Commands:     []string{"stagefreight ci run publish"},
				Source:       model.SourceSpec{FullClone: true},
				Capabilities: model.CapabilitySpec{PackageRegistries: packageRegistries(cfg)},
				Policy:       model.PolicySpec{AllowFailure: true},
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

// packageRegistries lists the registries the publish job may push to, by config
// provider + credential env prefix, so each forge emitter can auto-wire the one(s)
// native to it (github→ghcr, gitea→gitea, …) with the forge's auto-token. Only
// registries declaring a credentials prefix qualify — the prefix names the env vars
// (<PREFIX>_USER / <PREFIX>_TOKEN) the forge supplies values for.
func packageRegistries(cfg *config.Config) []model.PackageRegistry {
	var out []model.PackageRegistry
	for _, r := range cfg.Registries {
		if r.Credentials == "" {
			continue
		}
		out = append(out, model.PackageRegistry{Provider: r.Provider, CredPrefix: r.Credentials})
	}
	return out
}
