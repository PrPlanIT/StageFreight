package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Level controls how much of the codebase gets scanned.
type Level string

const (
	LevelChanged Level = "changed"
	LevelFull    Level = "full"
)

// ModuleConfig holds per-module overrides. Options are authored INLINE (module-specific
// keys sit directly under the module); the retired `options:` wrapper is gone. Enabled and
// Exclude are the two typed fields every module shares; any other key is a module-specific
// option collected into Options (the heterogeneous bag the lint engine reads wholesale).
type ModuleConfig struct {
	Enabled *bool
	Exclude []string
	Options map[string]any
}

// UnmarshalYAML flattens the module surface: `enabled`/`exclude` become the typed fields,
// every other key is a module option → Options. A stray `options:` key is the retired
// wrapper and is rejected loudly (rather than silently nesting under Options["options"]).
func (m *ModuleConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("module must be a mapping")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case "enabled":
			var b bool
			if err := val.Decode(&b); err != nil {
				return fmt.Errorf("enabled: %w", err)
			}
			m.Enabled = &b
		case "exclude":
			if err := val.Decode(&m.Exclude); err != nil {
				return fmt.Errorf("exclude: %w", err)
			}
		case "options":
			return fmt.Errorf("the `options:` wrapper was removed — inline the option keys directly under the module")
		default:
			if m.Options == nil {
				m.Options = map[string]any{}
			}
			var v any
			if err := val.Decode(&v); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			m.Options[key] = v
		}
	}
	return nil
}

// MarshalYAML re-flattens: enabled/exclude plus the option keys at the module's top level,
// so `config render` shows the inline shape rather than the internal struct.
func (m ModuleConfig) MarshalYAML() (any, error) {
	out := map[string]any{}
	if m.Enabled != nil {
		out["enabled"] = *m.Enabled
	}
	if len(m.Exclude) > 0 {
		out["exclude"] = m.Exclude
	}
	for k, v := range m.Options {
		out[k] = v
	}
	return out, nil
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

	// FailOn is the DIAGNOSTIC-IMPORTANCE threshold at or above which a lint
	// finding blocks the build: "critical" | "warning" | "info" | "off". This is
	// lint's OWN ordered vocabulary (info < warning < critical) — deliberately NOT
	// the vulnerability severity scale; a lint warning and a CVSS High are
	// incomparable. Empty defaults to "critical" (today's behavior: only critical
	// findings block). Note this is a SECOND control axis on top of each module's
	// own severity: raising fail_on reclassifies lower-importance findings from
	// other lint modules as blocking too.
	FailOn string `yaml:"fail_on,omitempty"`
}

// EffectiveFailOn resolves the lint gate threshold, defaulting to "critical"
// (today's behavior). Returns a lowercased "critical" | "warning" | "info" |
// "off". The lint package maps this onto its Severity tiers.
func (c LintConfig) EffectiveFailOn() string {
	if v := strings.ToLower(strings.TrimSpace(c.FailOn)); v != "" {
		return v
	}
	return "critical"
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
