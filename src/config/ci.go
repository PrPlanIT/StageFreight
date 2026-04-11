package config

// CIConfig holds all pipeline-related configuration consumed by
// stagefreight ci render. One block, one concern.
type CIConfig struct {
	// Image is the container image for all pipeline jobs.
	// Required — render refuses to emit without it.
	Image string `yaml:"image"`

	// Routing declares per-phase runner placement requirements.
	// The renderer lowers labels to forge-native primitives
	// (GitLab: tags, GitHub/Gitea/Forgejo: runs-on).
	Routing RoutingConfig `yaml:"routing,omitempty"`
}

// RoutingConfig declares per-phase routing requirements.
type RoutingConfig struct {
	Perform RoutingSpec `yaml:"perform,omitempty"`
}

// RoutingSpec declares runner placement labels for a single phase.
type RoutingSpec struct {
	Labels []string `yaml:"labels,omitempty"`
}
