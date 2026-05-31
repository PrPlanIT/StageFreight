package build

import (
	"time"
)

// BuildResult captures the outcome of a full build plan execution.
type BuildResult struct {
	Steps    []StepResult
	Duration time.Duration
}

// StepResult captures the outcome of a single build step.
type StepResult struct {
	Name         string
	Status       string            // "success", "failed", "cached"
	Images       []string          // pushed image references (raw refs for display)
	Publications []PushObservation // structured publish records — v2 wire to ResultsBuilder
	Artifacts    []string          // extracted file paths
	Layers       []LayerEvent      // parsed build layer events (from --progress=plain)
	Duration     time.Duration
	Error        error
}

// PushObservation is buildx's record of one push: registry coordinates plus
// the observed digest. Scoped to docker push observation — consumers convert
// to v2 Outcome by calling rb.Record with these fields. Does NOT carry
// credentials, provider, or any value used as a join key; ArtifactID lives
// with the consumer that knows which step produced this observation.
type PushObservation struct {
	Host   string
	Path   string
	Tag    string
	Digest string
}
