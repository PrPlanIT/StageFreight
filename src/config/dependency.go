package config

// DependencyConfig holds configuration for the dependency update subsystem.
type DependencyConfig struct {
	Enabled bool                  `yaml:"enabled"`
	Output  string                `yaml:"output"`
	Scope   DependencyScopeConfig `yaml:"scope"`
	Commit  DependencyCommitConfig `yaml:"commit"`
}

// DependencyScopeConfig controls which dependency ecosystems are managed.
type DependencyScopeConfig struct {
	GoModules    bool `yaml:"go_modules"`
	DockerfileEnv bool `yaml:"dockerfile_env"` // umbrella for docker-image + docker-tool
}

// DependencyCommitConfig controls auto-commit behavior for dependency updates.
type DependencyCommitConfig struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`
	Message string `yaml:"message"`
	Push    bool   `yaml:"push"`
	SkipCI  bool   `yaml:"skip_ci"`
}

// DefaultDependencyConfig returns sensible defaults for dependency management.
func DefaultDependencyConfig() DependencyConfig {
	return DependencyConfig{
		Enabled: true,
		Output:  ".stagefreight/deps",
		Scope: DependencyScopeConfig{
			GoModules:    true,
			DockerfileEnv: true,
		},
		Commit: DependencyCommitConfig{
			Enabled: true,
			Type:    "chore",
			Message: "update managed dependencies",
			Push:    true,
			SkipCI:  true,
		},
	}
}

// ScopeToEcosystems converts scope booleans to ecosystem filter strings
// compatible with dependency.UpdateConfig.Ecosystems.
// Returns nil (all ecosystems) if all scopes are enabled.
func (s DependencyScopeConfig) ScopeToEcosystems() []string {
	if s.GoModules && s.DockerfileEnv {
		return nil // all
	}
	var ecosystems []string
	if s.GoModules {
		ecosystems = append(ecosystems, "gomod")
	}
	if s.DockerfileEnv {
		ecosystems = append(ecosystems, "docker-image", "docker-tool")
	}
	return ecosystems
}
