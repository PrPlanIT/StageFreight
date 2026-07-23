package config

import "github.com/PrPlanIT/StageFreight/src/paths"

// ReleaseConfig holds configuration for the release subsystem.
type ReleaseConfig struct {
	Preset          string `yaml:"preset,omitempty"`
	Enabled         bool   `yaml:"enabled"`
	Required        *bool  `yaml:"required,omitempty"` // failure is hard pipeline fail (default: false)
	SecuritySummary string `yaml:"security_summary"`
	RegistryLinks   bool   `yaml:"registry_links"`
	CatalogLinks    bool   `yaml:"catalog_links"`
	// Render controls release rendering (was presentation.release). Pointer: nil
	// preserves the default; set overrides. Folded into Presentation.Release.
	Render  *ReleasePresentation `yaml:"render,omitempty"`
	RunFrom RunFromConfig        `yaml:"run_from,omitempty"` // gate mutation to declared origin
}

// IsRequired returns whether release failure is a hard pipeline fail. Default: false.
func (r ReleaseConfig) IsRequired() bool {
	if r.Required != nil {
		return *r.Required
	}
	return false
}

// DefaultReleaseConfig returns sensible defaults for release management.
func DefaultReleaseConfig() ReleaseConfig {
	return ReleaseConfig{
		Enabled:         true,
		SecuritySummary: paths.Ephemeral("", "security"),
		RegistryLinks:   true,
		CatalogLinks:    true,
	}
}
