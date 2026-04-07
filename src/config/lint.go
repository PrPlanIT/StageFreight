package config

// Level controls how much of the codebase gets scanned.
type Level string

const (
	LevelChanged Level = "changed"
	LevelFull    Level = "full"
)

// ModuleConfig holds per-module overrides.
type ModuleConfig struct {
	Enabled *bool          `yaml:"enabled,omitempty"`
	Exclude []string       `yaml:"exclude,omitempty"`
	Options map[string]any `yaml:"options,omitempty"`
}

// LintConfig holds lint-specific configuration.
type LintConfig struct {
	Preset       string                  `yaml:"preset,omitempty"`
	Level        Level                   `yaml:"level"`
	CacheDir     string                  `yaml:"cache_dir"`
	TargetBranch string                  `yaml:"target_branch"`
	Exclude      []string                `yaml:"exclude"`
	Modules      map[string]ModuleConfig `yaml:"modules"`
	Cache        LintCacheConfig         `yaml:"cache,omitempty"`
}

// LintCacheConfig controls lint result cache lifecycle.
// Content-addressed caches grow monotonically — every file edit creates
// a new entry. Without eviction, cache grows unbounded.
type LintCacheConfig struct {
	MaxAge  string `yaml:"max_age,omitempty"`  // evict entries not hit in this duration (e.g. "7d")
	MaxSize string `yaml:"max_size,omitempty"` // evict oldest entries when cache exceeds this (e.g. "100MB")
}

// DefaultLintConfig returns production defaults.
func DefaultLintConfig() LintConfig {
	return LintConfig{
		Level:   LevelChanged,
		Exclude: []string{},
		Modules: map[string]ModuleConfig{},
		Cache: LintCacheConfig{
			MaxAge:  "7d",
			MaxSize: "100MB",
		},
	}
}
