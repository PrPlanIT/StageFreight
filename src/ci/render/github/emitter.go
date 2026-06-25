// Package github renders a StageFreight pipeline to a GitHub Actions workflow.
//
// This package is GitHub's first-class identity in the render layer. It owns
// GitHub's lowering decisions; today that is "serialize as an Actions workflow,"
// delegated to the private actions backend, but any GitHub-specific divergence
// (reusable workflows, environments, fine-grained permissions) lands here without
// touching any other forge.
package github

import (
	"github.com/PrPlanIT/StageFreight/src/ci/render/internal/actions"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Emit renders the pipeline to a GitHub Actions workflow.
func Emit(p model.Pipeline) ([]byte, error) {
	return actions.Emit(p, actions.Dialect{
		Provider: "github",
		// ghcr.io authenticates with the auto-provided GITHUB_TOKEN (packages:
		// write) — no user-configured secret. Pushing to GitHub's own registry on
		// Actions is therefore turnkey.
		NativeRegistries: []string{"ghcr", "github"},
		PackageAuth: &actions.PackageAuth{
			Permission: "packages: write",
			User:       "${{ github.actor }}",
			Token:      "${{ secrets.GITHUB_TOKEN }}",
		},
		// Forge REST API (releases, MRs): the client reads GITHUB_TOKEN. Operator PAT
		// (secrets.GH_TOKEN, repo scope) overrides the auto-token, which on a fork is
		// often read-only and 401s on release creation.
		ForgeAPIAuth: &actions.ForgeAPIAuth{
			EnvVar: "GITHUB_TOKEN",
			Value:  "${{ secrets.GH_TOKEN || secrets.GITHUB_TOKEN }}",
		},
	})
}
