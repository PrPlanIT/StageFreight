package config

// LifecycleConfig selects the repository lifecycle mode — the phase graph the
// pipeline runs. Mode is the single most significant orchestration choice:
// review and publish are image-only; the other modes mark them not_applicable.
type LifecycleConfig struct {
	// Preset references an external lifecycle fragment to inherit (the generic
	// preset: fragment-include mechanism — resolved and deep-merged before parse).
	Preset string `yaml:"preset,omitempty"`
	// Mode selects the phase graph. Empty defaults to image.
	//   image      — build → review → publish image pipeline (the default)
	//   docker     — read-only Docker plan/reconcile (dry-run)
	//   gitops     — validate + reconcile Flux manifests
	//   governance — governance control-repo reconcile
	Mode string `yaml:"mode"`
}

// GovernanceConfig declares governance clusters for the control repo.
// Only valid when lifecycle.mode is "governance".
// Assets (CI skeletons, settings files, etc.) are declared inside each
// cluster's stagefreight config as assets: entries — no separate skeleton construct.
type GovernanceConfig struct {
	Clusters []GovernanceCluster `yaml:"clusters"`
}

// GovernanceCluster assigns lifecycle doctrine to a group of repos.
type GovernanceCluster struct {
	ID           string                   `yaml:"id"`
	Targets      GovernanceClusterTargets `yaml:"targets"`
	StageFreight map[string]any           `yaml:"stagefreight"`
}

// GovernanceClusterTargets identifies which repos belong to this cluster.
// Supports flat repos list and/or grouped targets with per-group forge identity.
type GovernanceClusterTargets struct {
	Repos       []string                 `yaml:"repos,omitempty"`
	Groups      []GovernanceClusterGroup `yaml:"groups,omitempty"`
	Credentials string                   `yaml:"credentials,omitempty"` // env var prefix for forge auth
}

// GovernanceClusterGroup is a cohort of repos on the same forge.
type GovernanceClusterGroup struct {
	ID    string   `yaml:"id,omitempty"`
	Repos []string `yaml:"repos"`
}
