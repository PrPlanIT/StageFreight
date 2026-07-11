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

// ToolConstraint declares the acceptable version(s) for a single tool — a
// predicate over versions, not necessarily one exact version (e.g. "1.26.4" is
// exact; "1.26.x" is a line). SHA256 is optional verification material for tools
// whose upstream doesn't publish a checksum manifest our resolver can consume
// (e.g. cargo-llvm-cov ships BLAKE3). The digest is MUTABLE PROJECT STATE: deps
// derives and rewrites it in lockstep with the resolved version (transactional
// upgrade), and the runtime resolver verifies downloaded bytes against it. Because
// one digest authenticates exactly one artifact, SHA256 is only meaningful with an
// exact constraint (validated). Empty SHA256 means "verify via upstream ChecksumURL".
type ToolConstraint struct {
	Constraint string `yaml:"constraint,omitempty"`
	SHA256     string `yaml:"sha256,omitempty"`
}

// UnmarshalYAML accepts three input forms and NORMALIZES them to one internal
// concept (Constraint) — it answers only "what did the user write", performing no
// semantic validation (that is a separate phase):
//
//	go: 1.26.x                       # scalar shorthand → constraint
//	go: {constraint: 1.26.x}         # explicit
//	go: {version: 1.26.4}            # legacy alias, normalized to constraint
//
// The one structural rule enforced here (because it needs both raw keys, which only
// the loader sees): `constraint` and `version` are mutually exclusive.
func (t *ToolConstraint) UnmarshalYAML(node *yaml.Node) error {
	// Scalar shorthand desugars to constraint (never to the legacy `version`).
	if node.Kind == yaml.ScalarNode {
		t.Constraint = node.Value
		return nil
	}
	var raw struct {
		Constraint string `yaml:"constraint"`
		Version    string `yaml:"version"` // legacy alias, normalized away here
		SHA256     string `yaml:"sha256"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if raw.Constraint != "" && raw.Version != "" {
		return fmt.Errorf("toolchain entry sets both 'constraint' and 'version'; use one ('version' is the legacy name for 'constraint')")
	}
	t.Constraint = raw.Constraint
	if t.Constraint == "" {
		t.Constraint = raw.Version // legacy alias
	}
	t.SHA256 = raw.SHA256
	return nil
}
