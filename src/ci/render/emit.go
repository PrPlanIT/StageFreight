package render

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/ci/render/azuredevops"
	"github.com/PrPlanIT/StageFreight/src/ci/render/forgejo"
	"github.com/PrPlanIT/StageFreight/src/ci/render/gitea"
	"github.com/PrPlanIT/StageFreight/src/ci/render/github"
	"github.com/PrPlanIT/StageFreight/src/ci/render/gitlab"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// SupportedForges lists forges with render support.
var SupportedForges = []string{"gitlab", "github", "gitea", "forgejo", "azuredevops"}

// Emit dispatches to the appropriate forge's emitter. One forge, one package,
// one identity — the dispatch names forges only, never a serialization backend.
// Returns an error for unsupported forges — no silent skipping.
func Emit(forge string, p model.Pipeline) ([]byte, error) {
	switch forge {
	case "gitlab":
		return gitlab.Emit(p)
	case "github":
		return github.Emit(p)
	case "gitea":
		return gitea.Emit(p)
	case "forgejo":
		return forgejo.Emit(p)
	case "azuredevops":
		return azuredevops.Emit(p)
	default:
		return nil, fmt.Errorf("unsupported forge %q (supported: %v)", forge, SupportedForges)
	}
}
