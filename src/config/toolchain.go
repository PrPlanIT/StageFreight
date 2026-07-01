package config

// ToolchainConfig defines operator control over external tool resolution.
// Top-level in .stagefreight.yml because toolchains are execution substrate,
// not CI-specific — they run in security scanning, signing, linting, gitops.
type ToolchainConfig struct {
	// Desired declares intended tool versions. Authoritative — not a hint.
	// If a desired version fails to resolve, the system fails. No fallback.
	Desired map[string]ToolPinConfig `yaml:"desired,omitempty"`
}

// ToolPinConfig declares the desired version for a single tool — and, for tools
// whose upstream doesn't publish a SHA256 checksum manifest our resolver can consume
// (e.g. cargo-llvm-cov ships BLAKE3), the pinned artifact digest. The digest is
// MUTABLE PROJECT STATE: deps derives and rewrites it in lockstep with the version
// (transactional upgrade), and the runtime resolver verifies downloaded bytes
// against it. Empty SHA256 means "verify via the tool's upstream ChecksumURL."
type ToolPinConfig struct {
	Version string `yaml:"version,omitempty"`
	SHA256  string `yaml:"sha256,omitempty"`
}
