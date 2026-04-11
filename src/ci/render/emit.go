package render

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/ci/render/gitlab"
	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// SupportedForges lists forges with render support.
var SupportedForges = []string{"gitlab"}

// Emit dispatches to the appropriate forge emitter.
// Returns an error for unsupported forges — no silent skipping.
func Emit(forge string, p model.Pipeline) ([]byte, error) {
	switch forge {
	case "gitlab":
		return gitlab.Emit(p)
	default:
		return nil, fmt.Errorf("unsupported forge %q (supported: %v)", forge, SupportedForges)
	}
}
