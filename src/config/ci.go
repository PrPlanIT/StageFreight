package config

// CIConfig holds all pipeline-related configuration consumed by
// stagefreight ci render. One block, one concern.
type CIConfig struct {
	// Image is the container image for all pipeline jobs.
	// Required — render refuses to emit without it.
	Image string `yaml:"image"`

	// Forges names which forges' CI skeletons this repo runs on, and therefore which
	// pipeline files governance renders and distributes (gitlab → .gitlab-ci.yml,
	// github → .github/workflows/stagefreight.yml, …). Each forge writes its own file,
	// so declaring several is well-defined — a repo genuinely wired to two CI systems
	// says so rather than hand-maintaining the second.
	//
	// It is DECLARED, never inferred. The primary repo's forge is the obvious guess and
	// it is wrong often enough to matter: a repo can be authoritative on one forge and
	// run CI on another after replication — source on GitLab, Actions on the GitHub
	// mirror. Guessing there writes a skeleton for a forge that never runs it while the
	// one that does goes stale, surfacing as an unexplained "CI is stale" at audition.
	//
	// Empty means no CI file is rendered or distributed. That is deliberate: a repo that
	// has not said where its CI runs gets nothing, rather than something plausible.
	//
	// A caution rather than a rule, since this is the operator's call: two forges running
	// the SAME lifecycle both publish. Where that is not wanted, the phases each pipeline
	// runs are already gated by the usual when: conditions.
	Forges []string `yaml:"forges,omitempty"`

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
