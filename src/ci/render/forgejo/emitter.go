// Package forgejo renders a StageFreight pipeline to a Forgejo Actions workflow.
//
// This package is Forgejo's first-class identity in the render layer. It owns
// Forgejo's lowering decisions; today that is delegated to the private actions
// backend, but any Forgejo-specific divergence (its own OIDC behavior, package
// registry, federation) lands here without touching any other forge.
package forgejo

import (
	"github.com/PrPlanIT/StageFreight/src/ci/render/internal/actions"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Emit renders the pipeline to a Forgejo Actions workflow.
func Emit(p model.Pipeline) ([]byte, error) {
	return actions.Emit(p, actions.Dialect{
		Provider: "forgejo",
		// Forgejo's built-in package registry authenticates with the runner's
		// auto-provided GITHUB_TOKEN (Forgejo Actions is Actions-compatible) — no
		// configured secret. "gitea" is accepted too since Forgejo's registry is
		// Gitea-compatible.
		NativeRegistries: []string{"forgejo", "gitea"},
		PackageAuth: &actions.PackageAuth{
			Permission: "packages: write",
			User:       "${{ github.actor }}",
			Token:      "${{ secrets.GITHUB_TOKEN }}",
		},
	})
}
