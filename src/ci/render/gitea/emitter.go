// Package gitea renders a StageFreight pipeline to a Gitea Actions workflow.
//
// This package is Gitea's first-class identity in the render layer. It owns
// Gitea's lowering decisions; today that is delegated to the private actions
// backend, but any Gitea-specific divergence (act_runner label/scheduling
// behavior) lands here without touching any other forge.
package gitea

import (
	"github.com/PrPlanIT/StageFreight/src/ci/render/internal/actions"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Emit renders the pipeline to a Gitea Actions workflow.
func Emit(p model.Pipeline) ([]byte, error) {
	return actions.Emit(p, actions.Dialect{
		Provider: "gitea",
		// Gitea's built-in container registry authenticates with act_runner's
		// auto-provided GITHUB_TOKEN (Gitea is Actions-compatible) — no configured
		// secret.
		NativeRegistries: []string{"gitea"},
		PackageAuth: &actions.PackageAuth{
			Permission: "packages: write",
			User:       "${{ github.actor }}",
			Token:      "${{ secrets.GITHUB_TOKEN }}",
		},
		// Gitea's API client reads GITEA_TOKEN; operator override falls back to the
		// act_runner auto-token.
		ForgeAPIAuth: &actions.ForgeAPIAuth{
			EnvVar: "GITEA_TOKEN",
			Value:  "${{ secrets.GITEA_TOKEN || secrets.GITHUB_TOKEN }}",
		},
	})
}
