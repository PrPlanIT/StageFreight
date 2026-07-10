package config

import "strings"

// ScannersConfig controls which vulnerability scanners are enabled.
// Both default to true — scanners still require their binary in PATH.
// Uses *bool so omitting a key preserves the default instead of zeroing it.
type ScannersConfig struct {
	Trivy *bool `yaml:"trivy,omitempty"` // run Trivy image scan (default: true)
	Grype *bool `yaml:"grype,omitempty"` // run Grype image scan (default: true)
}

// TrivyEnabled returns whether Trivy scanning is enabled (default: true).
func (s ScannersConfig) TrivyEnabled() bool {
	if s.Trivy == nil {
		return true
	}
	return *s.Trivy
}

// GrypeEnabled returns whether Grype scanning is enabled (default: true).
func (s ScannersConfig) GrypeEnabled() bool {
	if s.Grype == nil {
		return true
	}
	return *s.Grype
}

// SecurityConfig holds security scanning configuration.
type SecurityConfig struct {
	Preset         string         `yaml:"preset,omitempty"`
	Enabled        bool           `yaml:"enabled"`          // run vulnerability scanning (default: true)
	Required       *bool          `yaml:"required,omitempty"` // failure is hard pipeline fail (default: false)
	Scanners       ScannersConfig `yaml:"scanners"`         // per-scanner toggles
	SBOMEnabled    bool           `yaml:"sbom"`             // generate SBOM artifacts (default: true)
	FailOnCritical bool           `yaml:"fail_on_critical"` // DEPRECATED: use fail_on. Alias — true → fail_on: critical.
	OutputDir      string         `yaml:"output"`           // directory for scan artifacts (default: .stagefreight/security)

	// FailOn is the severity threshold at or above which the scan fails the build:
	// "critical" | "high" | "medium" | "low" | "off". Empty falls back to the
	// deprecated fail_on_critical (true → "critical"), then to "off" — so the
	// default stays informational (no gate), exactly as today.
	FailOn string `yaml:"fail_on,omitempty"`

	// UnreachableVulns is the policy for vulnerabilities a reachability analyzer
	// proved are never called: "pass" (default — excused from the gate) or "fail"
	// (gated on severity regardless of reachability). Only vulns with a
	// proven-unreachable verdict are affected; everything else is "unknown" and
	// gates normally.
	UnreachableVulns string `yaml:"unreachable_vulns,omitempty"`

	// ReleaseDetail is the default detail level for security info in release notes.
	// Values: "none", "counts", "detailed", "full" (default: "counts").
	ReleaseDetail string `yaml:"release_detail"`

	// ReleaseDetailRules are conditional overrides evaluated top-down (first match wins).
	// Uses the standard Condition primitive for tag/branch matching with ! negation.
	ReleaseDetailRules []DetailRule `yaml:"release_detail_rules"`

	// Cache controls persistent vulnerability DB caching per scanner.
	// Each tool's max_size triggers full-clear when exceeded (opaque DBs, no granular eviction).
	// Empty/omitted per tool = no StageFreight-managed persistence for that tool.
	Cache SecurityCacheConfig `yaml:"cache,omitempty"`

	// OverwhelmMessage is the message lines shown when >1000 vulns are found.
	// Defaults to ["…maybe start here:"] with the OverwhelmLink below.
	OverwhelmMessage []string `yaml:"overwhelm_message"`

	// OverwhelmLink is an optional URL appended after OverwhelmMessage.
	// Defaults to a Psychology Today anxiety page. Empty string disables.
	OverwhelmLink string `yaml:"overwhelm_link"`
}

// SecurityCacheConfig controls persistent vuln DB caching per scanner tool.
// These are opaque tool-managed directories — StageFreight hosts and bounds them,
// but does not manage their internal structure.
type SecurityCacheConfig struct {
	Trivy ScannerCacheConfig `yaml:"trivy,omitempty"`
	Grype ScannerCacheConfig `yaml:"grype,omitempty"`
}

// ScannerCacheConfig is the cache policy for a single scanner.
// Any field set = persistent cache enabled for this tool.
// All fields empty = no StageFreight-managed persistence (tool defaults).
// Eviction is full-clear (opaque DBs, no granular eviction).
type ScannerCacheConfig struct {
	MaxSize string `yaml:"max_size,omitempty"` // e.g. "500MB" — full-clear when exceeded
	MaxAge  string `yaml:"max_age,omitempty"`  // e.g. "7d" — full-clear when oldest file exceeds age
}

// DetailRule is a conditional override for security detail level in release notes.
// Embeds Condition for standard tag/branch pattern matching.
type DetailRule struct {
	Condition `yaml:",inline"`

	// Detail is the detail level to use when this rule matches.
	// Values: "none", "counts", "detailed", "full".
	Detail string `yaml:"detail"`
}

// IsRequired returns whether security failure is a hard pipeline fail. Default: false.
func (s SecurityConfig) IsRequired() bool {
	if s.Required != nil {
		return *s.Required
	}
	return false
}

// EffectiveFailOn resolves the severity threshold at or above which the security
// scan fails the build. Precedence: explicit fail_on > the deprecated
// fail_on_critical bool (true → "critical") > "off" — so with neither set the
// scan stays informational, exactly as it does today. Returns a lowercased
// "critical" | "high" | "medium" | "low" | "off".
func (s SecurityConfig) EffectiveFailOn() string {
	if v := strings.ToLower(strings.TrimSpace(s.FailOn)); v != "" {
		return v
	}
	if s.FailOnCritical {
		return "critical"
	}
	return "off"
}

// UnreachablePolicy resolves how proven-unreachable vulnerabilities are gated:
// "pass" (default — excused) or "fail".
func (s SecurityConfig) UnreachablePolicy() string {
	if v := strings.ToLower(strings.TrimSpace(s.UnreachableVulns)); v != "" {
		return v
	}
	return "pass"
}

// DefaultSecurityConfig returns sensible defaults for security scanning.
func DefaultSecurityConfig() SecurityConfig {
	t := true
	return SecurityConfig{
		Enabled:        true,
		Scanners:       ScannersConfig{Trivy: &t, Grype: &t},
		SBOMEnabled:    true,
		FailOnCritical: false,
		OutputDir:      ".stagefreight/security",
		ReleaseDetail:  "counts",
	}
}
