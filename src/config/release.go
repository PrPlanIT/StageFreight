package config

// ReleaseConfig holds configuration for the release subsystem.
type ReleaseConfig struct {
	Enabled         bool   `yaml:"enabled"`
	SecuritySummary string `yaml:"security_summary"`
	RegistryLinks   bool   `yaml:"registry_links"`
	CatalogLinks    bool   `yaml:"catalog_links"`
}

// DefaultReleaseConfig returns sensible defaults for release management.
func DefaultReleaseConfig() ReleaseConfig {
	return ReleaseConfig{
		Enabled:         true,
		SecuritySummary: ".stagefreight/security",
		RegistryLinks:   true,
		CatalogLinks:    true,
	}
}
