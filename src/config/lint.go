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
	Provenance   ProvenanceConfig        `yaml:"provenance,omitempty"`
	Remediation  RemediationConfig       `yaml:"remediation,omitempty"`
	Cache        LintCacheConfig         `yaml:"cache,omitempty"`
}

// ProvenanceConfig lets a project DECLARE file provenance that can't be inferred from
// bytes or paths (e.g. a build's CSS output). Declarations are the highest-confidence
// provenance signal — they outrank heuristics — because only the project knows its own
// build. Globs match the repo-relative path. Provenance only ever RELAXES authored-code
// hygiene (whitespace/line-endings/length); security and supply-chain checks ignore it.
type ProvenanceConfig struct {
	Generated []string `yaml:"generated,omitempty"`
	Vendored  []string `yaml:"vendored,omitempty"`
}

// RemediationConfig is the granular opt-in for `--fix-safe`. Each field is a *bool: nil
// means "use the safe default" (on for the conservative hygiene fixes), so the one-shot
// `--fix-safe` flow works out of the box while a project can still disable a category.
// Categories that are policy-dependent (line endings, tab/space) default OFF and must be
// turned on explicitly. Only authored files are ever mutated — generated/vendored/lock
// emit no hygiene findings, so they carry no fixes.
type RemediationConfig struct {
	TrailingWhitespace *bool `yaml:"trailing_whitespace,omitempty"` // default ON under --fix-safe
	FinalNewline       *bool `yaml:"final_newline,omitempty"`       // default ON under --fix-safe
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
