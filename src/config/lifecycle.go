package config

// LifecycleConfig defines the repository lifecycle mode.
type LifecycleConfig struct {
	Mode string `yaml:"mode"` // image | gitops
}
