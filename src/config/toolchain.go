package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Toolchain resolution follows a three-responsibility split:
//
//	Constraints describe acceptable versions.
//	Selection chooses one acceptable version.
//	Verification authenticates the selected artifact.
//
// This package owns the first: parsing and validating the operator-declared
// constraint. Selection (which member of the candidate set to adopt) and
// verification (SHA256 of the chosen artifact) live downstream.

// ToolchainConfig defines operator control over external tool resolution.
// Top-level in .stagefreight.yml because toolchains are execution substrate,
// not CI-specific — they run in security scanning, signing, linting, gitops.
type ToolchainConfig struct {
	// Desired declares intended tool constraints. Authoritative — not a hint.
	// If a desired constraint fails to resolve, the system fails. No fallback.
	Desired map[string]ToolConstraint `yaml:"desired,omitempty"`
}

// UnmarshalYAML iterates the desired mapping at the map level so a parse error can
// name the offending tool (toolchains.desired.<name>) — the per-entry unmarshaler
// alone cannot, since it never sees its own map key.
func (c *ToolchainConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Desired yaml.Node `yaml:"desired"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if raw.Desired.Kind == 0 {
		return nil // no desired section
	}
	if raw.Desired.Kind != yaml.MappingNode {
		return fmt.Errorf("toolchains.desired: must be a mapping of tool -> constraint")
	}
	c.Desired = make(map[string]ToolConstraint, len(raw.Desired.Content)/2)
	for i := 0; i+1 < len(raw.Desired.Content); i += 2 {
		name := raw.Desired.Content[i].Value
		tc, err := parseToolConstraint(raw.Desired.Content[i+1])
		if err != nil {
			return fmt.Errorf("toolchains.desired.%s: %w", name, err)
		}
		c.Desired[name] = tc
	}
	return nil
}

// ToolConstraint declares the acceptable version(s) for a single tool. The YAML key is
// `version` — the Cargo/Go convention — and its VALUE is a version requirement, not
// necessarily one exact version: "1.26.4" is exact, "1.26.x" is a line. It is pure
// operator INTENT. The machine-maintained RESOLUTION of that intent — the concrete
// version a wildcard locked to, plus its artifact digest — lives in
// .stagefreight/toolchains.lock, NOT here: the config is the Cargo.toml, the lock is the
// Cargo.lock. The Go field is named Constraint because the value is a requirement (the
// same reason Cargo models a `version` field as a VersionReq), even though the surface
// key is `version`.
type ToolConstraint struct {
	Constraint string `yaml:"version,omitempty"`
}

// UnmarshalYAML accepts two input forms and normalizes them to the internal Constraint —
// it answers only "what did the user write", performing no semantic validation:
//
//	go: 1.26.x               # scalar shorthand
//	go: {version: 1.26.x}    # explicit
func (t *ToolConstraint) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := parseToolConstraint(node)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// parseToolConstraint is the shared normalization core: scalar-or-map → one
// ToolConstraint. The map form's only key is `version` (Cargo convention); its value is a
// version requirement. A pre-lock inline `constraint`/`resolved`/`sha256` is silently
// ignored (node.Decode does not reject unknown keys) and superseded by the lock.
func parseToolConstraint(node *yaml.Node) (ToolConstraint, error) {
	var t ToolConstraint
	if node.Kind == yaml.ScalarNode {
		t.Constraint = node.Value
		return t, nil
	}
	var raw struct {
		Version string `yaml:"version"`
	}
	if err := node.Decode(&raw); err != nil {
		return t, err
	}
	t.Constraint = raw.Version
	return t, nil
}
