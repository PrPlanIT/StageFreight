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

// RoutingConfig declares runner placement per phase. Default applies to EVERY job; a
// per-phase field overrides it for that job. Empty ⇒ no constraint (any runner). This
// matters beyond consistency: GitLab's local runner cache is per-runner, so the
// build/toolchain caches StageFreight configures only actually persist across the
// pipeline when its phases land on the SAME runner — otherwise floating jobs re-provision
// toolchains every run. Set `default` to keep the whole pipeline on one runner.
type RoutingConfig struct {
	Default  RoutingSpec `yaml:"default,omitempty"`
	Audition RoutingSpec `yaml:"audition,omitempty"`
	Perform  RoutingSpec `yaml:"perform,omitempty"`
	Review   RoutingSpec `yaml:"review,omitempty"`
	Publish  RoutingSpec `yaml:"publish,omitempty"`
	Narrate  RoutingSpec `yaml:"narrate,omitempty"`
}

// For returns the runner labels for a phase: the per-phase override if set, else the
// Default. nil when neither is configured (the job is unconstrained).
func (r RoutingConfig) For(phase string) []string {
	var spec RoutingSpec
	switch phase {
	case "audition":
		spec = r.Audition
	case "perform":
		spec = r.Perform
	case "review":
		spec = r.Review
	case "publish":
		spec = r.Publish
	case "narrate":
		spec = r.Narrate
	}
	if len(spec.Labels) > 0 {
		return spec.Labels
	}
	return r.Default.Labels
}

// RoutingSpec declares runner placement labels for a single phase.
type RoutingSpec struct {
	Labels []string `yaml:"labels,omitempty"`
}
