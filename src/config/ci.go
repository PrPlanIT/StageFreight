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

	// Docker configures the Docker-in-Docker transport for image-building jobs.
	Docker DockerConfig `yaml:"docker,omitempty"`
}

// DockerConfig configures the dind transport for jobs that build images.
type DockerConfig struct {
	// TLS controls whether dind uses TLS (port 2376 + shared /certs) or plain TCP
	// (port 2375, DOCKER_TLS_CERTDIR=""). Tri-state: nil = the forge default
	// (github → non-TLS, since hosted runners can't share the dind cert volume;
	// gitlab/gitea/forgejo → TLS, since their runners do share /certs). Set
	// explicitly to override — true for a self-hosted GitHub runner that
	// provisions cert sharing, or false to opt out of TLS anywhere. The choice is
	// the operator's; the default just encodes the common runner environment.
	TLS *bool `yaml:"tls,omitempty"`
}

// RoutingConfig declares per-phase routing requirements.
type RoutingConfig struct {
	Perform RoutingSpec `yaml:"perform,omitempty"`
}

// RoutingSpec declares runner placement labels for a single phase.
type RoutingSpec struct {
	Labels []string `yaml:"labels,omitempty"`
}
