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

// ToolchainConfig is the toolchains: block — a tool→constraint map (tool name →
// desired version constraint). Authoritative, not a hint: a constraint that fails to
// resolve fails the run, no fallback. Top-level in .stagefreight.yml because toolchains
// are execution substrate (security scanning, signing, linting, gitops), not CI-specific.
type ToolchainConfig map[string]ToolConstraint

// UnmarshalYAML decodes the tool→constraint mapping directly, naming the offending tool
// on a parse error. The toolchains block IS the map — the retired `desired:` wrapper is
// gone (a legacy `toolchains: { desired: … }` now parses `desired` as a tool with no
// version and fails validation).
func (c *ToolchainConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == 0 {
		return nil // no toolchains section
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("toolchains: must be a mapping of tool -> constraint")
	}
	out := make(ToolchainConfig, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		tc, err := parseToolConstraint(node.Content[i+1])
		if err != nil {
			return fmt.Errorf("toolchains.%s: %w", name, err)
		}
		out[name] = tc
	}
	*c = out
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
