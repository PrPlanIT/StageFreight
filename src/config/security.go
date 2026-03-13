package config

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
	Enabled        bool           `yaml:"enabled"`          // run vulnerability scanning (default: true)
	Scanners       ScannersConfig `yaml:"scanners"`         // per-scanner toggles
	SBOMEnabled    bool           `yaml:"sbom"`             // generate SBOM artifacts (default: true)
	FailOnCritical bool           `yaml:"fail_on_critical"` // fail the pipeline if critical vulns found
	OutputDir      string         `yaml:"output"`           // directory for scan artifacts (default: .stagefreight/security)

	// ReleaseDetail is the default detail level for security info in release notes.
	// Values: "none", "counts", "detailed", "full" (default: "counts").
	ReleaseDetail string `yaml:"release_detail"`

	// ReleaseDetailRules are conditional overrides evaluated top-down (first match wins).
	// Uses the standard Condition primitive for tag/branch matching with ! negation.
	ReleaseDetailRules []DetailRule `yaml:"release_detail_rules"`

	// OverwhelmMessage is the message lines shown when >1000 vulns are found.
	// Defaults to ["…maybe start here:"] with the OverwhelmLink below.
	OverwhelmMessage []string `yaml:"overwhelm_message"`

	// OverwhelmLink is an optional URL appended after OverwhelmMessage.
	// Defaults to a Psychology Today anxiety page. Empty string disables.
	OverwhelmLink string `yaml:"overwhelm_link"`
}

// UnmarshalYAML accepts both "output" (canonical) and "output_dir" (deprecated)
// so that crucible self-build can bootstrap across the rename.
func (s *SecurityConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain SecurityConfig // avoid recursion
	var raw struct {
		plain       `yaml:",inline"`
		OutputDirV1 string `yaml:"output_dir"` // deprecated alias
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*s = SecurityConfig(raw.plain)
	if s.OutputDir == "" && raw.OutputDirV1 != "" {
		s.OutputDir = raw.OutputDirV1
	}
	return nil
}

// DetailRule is a conditional override for security detail level in release notes.
// Embeds Condition for standard tag/branch pattern matching.
type DetailRule struct {
	Condition `yaml:",inline"`

	// Detail is the detail level to use when this rule matches.
	// Values: "none", "counts", "detailed", "full".
	Detail string `yaml:"detail"`
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
