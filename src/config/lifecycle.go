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

// GovernanceConfig declares governance profiles for the control repo. Presence of
// profiles activates governance reconcile (presence-gated like ansible/gitops).
// Assets (CI skeletons, settings files, etc.) are declared inside each profile's
// stagefreight config as assets: entries — no separate skeleton construct.
type GovernanceConfig struct {
	Profiles []GovernanceProfile `yaml:"profiles"`
}

// GovernanceProfile assigns lifecycle doctrine to a group of repos.
type GovernanceProfile struct {
	ID      string                   `yaml:"id"`
	Targets GovernanceProfileTargets `yaml:"targets"`
	Config  map[string]any           `yaml:"config"` // the profile's shared StageFreight config
}

// GovernanceProfileTargets identifies which repos belong to this cluster.
// Supports flat repos list and/or grouped targets with per-group forge identity.
type GovernanceProfileTargets struct {
	Repos       []string                 `yaml:"repos,omitempty"`
	Groups      []GovernanceProfileGroup `yaml:"groups,omitempty"`
	Credentials string                   `yaml:"credentials,omitempty"` // env var prefix for forge auth
}

// GovernanceProfileGroup is a cohort of repos on the same forge.
type GovernanceProfileGroup struct {
	ID    string   `yaml:"id,omitempty"`
	Repos []string `yaml:"repos"`
}
