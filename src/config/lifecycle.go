package config

// LifecycleConfig defines the repository lifecycle mode.
type LifecycleConfig struct {
	Mode string `yaml:"mode"` // image | gitops | governance
}

// GovernanceConfig declares governance clusters for the control repo.
// Only valid when lifecycle.mode is "governance".
type GovernanceConfig struct {
	Skeleton GovernanceSkeletonConfig `yaml:"skeleton"`
	Clusters []GovernanceCluster      `yaml:"clusters"`
}

// GovernanceSkeletonConfig declares the CI skeleton source.
type GovernanceSkeletonConfig struct {
	Source GovernanceSkeletonSource `yaml:"source"`
}

// GovernanceSkeletonSource points to the skeleton file.
type GovernanceSkeletonSource struct {
	RepoURL       string `yaml:"repo_url"`
	Path          string `yaml:"path"`
	Ref           string `yaml:"ref"`
	AllowFloating bool   `yaml:"allow_floating"`
}

// GovernanceCluster assigns lifecycle doctrine to a group of repos.
type GovernanceCluster struct {
	ID           string                  `yaml:"id"`
	Targets      GovernanceClusterTargets `yaml:"targets"`
	StageFreight map[string]any          `yaml:"stagefreight"`
}

// GovernanceClusterTargets identifies which repos belong to this cluster.
type GovernanceClusterTargets struct {
	Repos []string `yaml:"repos"`
}
