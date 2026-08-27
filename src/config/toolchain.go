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

// ToolchainSection is the toolchains: block — a wrapper pairing the tool→constraint
// map (want:) with the versions' lifecycle policy (retention:), following the
// keyed-map-plus-sibling-policy pattern (scribe: files+commit, governance: profiles).
// The map moved under want: in the wrapper flag-day: retention is POLICY about the
// installed versions, not a tool, so it must not live inside the tool namespace.
type ToolchainSection struct {
	// Want is the tool→constraint map — the versions the operator WANTS (constraints, not
	// necessarily exact pins); the lock records what resolution GOT. Mirrors the internal
	// ToolchainDesired vocabulary. Authoritative, not a hint: a constraint that
	// fails to resolve fails the run, no fallback. Top-level in .stagefreight.yml
	// because toolchains are execution substrate (security scanning, signing, linting,
	// gitops), not CI-specific.
	Want ToolchainConfig `yaml:"want,omitempty"`

	// Retention governs how many resolved versions of each tool are RETAINED on the
	// runner (keep_last newest per tool; unset = engine default 2). The pinned
	// constraint's resolved version and the lock's resolution are always protected —
	// retention rotates superseded residue, never declared intent.
	Retention RetentionPolicy `yaml:"retention,omitempty"`
}

// ToolchainConfig is the want: map inside the toolchains section (tool name →
// desired version constraint).
type ToolchainConfig map[string]ToolConstraint

// UnmarshalYAML decodes the toolchains section, detecting the retired FLAT shape
// (tool→constraint at the top level, pre-wrapper) and naming the offending key with a
// migration hint — never a bare unknown-field error.
func (s *ToolchainSection) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == 0 {
		return nil // no toolchains section
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("toolchains: must be a mapping with want: (and optional retention:)")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch key := node.Content[i].Value; key {
		case "want":
			if err := s.Want.UnmarshalYAML(node.Content[i+1]); err != nil {
				return err
			}
		case "retention":
			if err := node.Content[i+1].Decode(&s.Retention); err != nil {
				return fmt.Errorf("toolchains.retention: %w", err)
			}
		default:
			return fmt.Errorf("toolchains.%s: toolchains: was a flat tool→constraint map; nest the tool map under toolchains.want: (retention: is the only other key)", key)
		}
	}
	return nil
}

// UnmarshalYAML decodes the tool→constraint mapping directly, naming the offending tool
// on a parse error. The tools block IS the map — the retired `desired:` wrapper is
// gone (a legacy `desired: …` key parses as a tool with no version and fails
// validation).
func (c *ToolchainConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == 0 {
		return nil // no tools map
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("toolchains.want: must be a mapping of tool -> constraint")
	}
	out := make(ToolchainConfig, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		tc, err := parseToolConstraint(node.Content[i+1])
		if err != nil {
			return fmt.Errorf("toolchains.want.%s: %w", name, err)
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
