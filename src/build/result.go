package build

import (
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// BuildResult captures the outcome of a full build plan execution.
type BuildResult struct {
	Steps    []StepResult
	Duration time.Duration
}

// StepResult captures the outcome of a single build step.
type StepResult struct {
	Name            string
	Status          string                   // "success", "failed", "cached"
	Images          []string                 // pushed image references (raw refs for display)
	PublishedImages []artifact.PublishedImage // structured publish records (authoritative)
	Artifacts       []string                 // extracted file paths
	Layers          []LayerEvent             // parsed build layer events (from --progress=plain)
	Duration        time.Duration
	Error           error
}
