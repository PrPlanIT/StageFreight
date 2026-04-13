package config

// ToolchainConfig defines operator control over external tool resolution.
// Top-level in .stagefreight.yml because toolchains are execution substrate,
// not CI-specific — they run in security scanning, signing, linting, gitops.
type ToolchainConfig struct {
	// Desired declares intended tool versions. Authoritative — not a hint.
	// If a desired version fails to resolve, the system fails. No fallback.
	Desired map[string]ToolPinConfig `yaml:"desired,omitempty"`
}

// ToolPinConfig declares the desired version for a single tool.
type ToolPinConfig struct {
	Version string `yaml:"version,omitempty"`
}
