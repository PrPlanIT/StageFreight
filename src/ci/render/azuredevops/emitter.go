// Package azuredevops renders a StageFreight pipeline to an Azure DevOps pipeline.
//
// This package is Azure DevOps's first-class identity in the render layer. It owns
// Azure's lowering decisions, delegating serialization to the private
// azurepipelines backend. Azure-specific divergence (service connections, variable
// groups, environments/approvals) lands here without touching any other forge.
package azuredevops

import (
	"github.com/PrPlanIT/StageFreight/src/ci/render/internal/azurepipelines"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// Emit renders the pipeline to an Azure DevOps azure-pipelines.yml.
func Emit(p model.Pipeline) ([]byte, error) {
	return azurepipelines.Emit(p, azurepipelines.Dialect{Provider: "azuredevops"})
}
