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
	return actions.Emit(p, actions.Dialect{Provider: "gitea"})
}
