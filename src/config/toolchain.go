package config

import (
	"fmt"

	depversion "github.com/PrPlanIT/StageFreight/src/supplychain/version"
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
	// Resolved is the concrete version a WILDCARD constraint is currently locked to —
	// machine-maintained (the update pass writes it, like a Cargo.lock entry). Normal
	// runs provision/verify this exact version, so a wildcard stays put until an update
	// pass deliberately moves the lock forward. Empty/unused for an exact constraint
	// (where Constraint is itself the version).
	Resolved string `yaml:"resolved,omitempty"`
	SHA256   string `yaml:"sha256,omitempty"`
}

// EffectiveVersion is the concrete version to provision and verify: the Constraint
// itself when it is exact, or the machine-locked Resolved version when the Constraint
// is a wildcard. Empty when a wildcard has no lock yet — the caller falls back to the
// tool default until an update pass resolves it. This is what makes a wildcard
// toolchain both continuous (same version between deliberate moves) and provisionable
// (a wildcard string like "1.26.x" is not a downloadable version).
func (t ToolConstraint) EffectiveVersion() string {
	if depversion.IsWildcardConstraint(t.Constraint) {
		return t.Resolved
	}
	return t.Constraint
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
	parsed, err := parseToolConstraint(node)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// parseToolConstraint is the shared normalization core: scalar-or-map → one
// ToolConstraint. Errors carry no tool name; callers with the map key (see
// ToolchainConfig.UnmarshalYAML) wrap them with toolchains.desired.<name>.
func parseToolConstraint(node *yaml.Node) (ToolConstraint, error) {
	var t ToolConstraint
	// Scalar shorthand desugars to constraint (never to the legacy `version`).
	if node.Kind == yaml.ScalarNode {
		t.Constraint = node.Value
		return t, nil
	}
	var raw struct {
		Constraint string `yaml:"constraint"`
		Version    string `yaml:"version"` // legacy alias, normalized away here
		Resolved   string `yaml:"resolved"`
		SHA256     string `yaml:"sha256"`
	}
	if err := node.Decode(&raw); err != nil {
		return t, err
	}
	if raw.Constraint != "" && raw.Version != "" {
		return t, fmt.Errorf("sets both 'constraint' and 'version'; use one ('version' is the legacy name for 'constraint')")
	}
	t.Constraint = raw.Constraint
	if t.Constraint == "" {
		t.Constraint = raw.Version // legacy alias
	}
	t.Resolved = raw.Resolved
	t.SHA256 = raw.SHA256
	return t, nil
}
