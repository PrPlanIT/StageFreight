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
		// GitHub-hosted runners can't share the dind cert volume with the job, so
		// dind defaults to plain TCP. A self-hosted runner with cert sharing can
		// flip it back on via ci.docker.tls: true.
		DindTLSDefault: false,
		PackageAuth: &actions.PackageAuth{
			Permission: "packages: write",
			User:       "${{ github.actor }}",
			Token:      "${{ secrets.GITHUB_TOKEN }}",
		},
	})
}
