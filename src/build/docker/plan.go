package docker

import (
	"github.com/PrPlanIT/StageFreight/src/build"
)

// applyImageBuildStrategy sets each image step's Load/Push from platform shape +
// flags. Invariant: under transport, publish is the sole distributor — steps never
// push, and a daemon copy is loaded only when the bytes aren't already CAS-retained
// (retainViaCAS); multi-platform pushes directly since buildx can't --load it.
func applyImageBuildStrategy(plan *build.BuildPlan, transport, local, retainViaCAS bool) {
	for i := range plan.Steps {
		step := &plan.Steps[i]
		switch {
		case local:
			step.Load = true
			step.Push = false
			if len(step.Tags) == 0 {
				step.Tags = []string{"stagefreight:dev"}
			}
		case len(step.Registries) == 0:
			step.Load = true
			if len(step.Tags) == 0 {
				step.Tags = []string{"stagefreight:dev"}
			}
		case transport:
			step.Push = false
			// Load a single-platform daemon copy ONLY when the artifact is not
			// retained via the CAS layout — there the daemon copy IS the retained
			// bytes. With CAS retention active (multi-platform's path), nothing
			// reads the daemon copy (digest=metadata, scan=layout, publish=cache),
			// so loading it just creates a second, unconsumed copy.
			if !IsMultiPlatform(*step) && !retainViaCAS {
				step.Load = true
			}
		case IsMultiPlatform(*step):
			step.Push = true
		default:
			step.Load = true
		}
	}
}
