package config

// CommitConfig holds configuration for the commit subsystem.
type CommitConfig struct {
	Preset       string       `yaml:"preset,omitempty"`
	DefaultType  string       `yaml:"default_type,omitempty"`
	DefaultScope string       `yaml:"default_scope,omitempty"`
	SkipCI       bool         `yaml:"skip_ci,omitempty"`
	Push         bool         `yaml:"push,omitempty"`
	Conventional bool         `yaml:"conventional"`
	Backend      string       `yaml:"backend,omitempty"`
	Types        []CommitType `yaml:"types,omitempty"`
}

// CommitType defines a recognized commit type for conventional commits.
type CommitType struct {
	Key       string `yaml:"key"`
	Label     string `yaml:"label"`
	AliasFor  string `yaml:"alias_for,omitempty"`
	ForceBang bool   `yaml:"force_bang,omitempty"`
}

// DefaultCommitConfig returns sensible defaults for commit configuration.
func DefaultCommitConfig() CommitConfig {
	return CommitConfig{
		DefaultType:  "chore",
		Conventional: true,
		Backend:      "git",
		Types:        defaultCommitTypes(),
	}
}

func defaultCommitTypes() []CommitType {
	return []CommitType{
		{Key: "feat", Label: "Feature"},
		{Key: "fix", Label: "Fix"},
		{Key: "docs", Label: "Documentation"},
		{Key: "chore", Label: "Chore"},
		{Key: "refactor", Label: "Refactor"},
		{Key: "ci", Label: "CI"},
		{Key: "perf", Label: "Performance"},
		{Key: "test", Label: "Test"},
		{Key: "revert", Label: "Revert"},
		{Key: "security", Label: "Security"},
		{Key: "build", Label: "Build"},
		{Key: "breaking", Label: "Breaking", ForceBang: true},
	}
}
