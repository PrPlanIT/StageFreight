package config

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
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

	// Update lets the dependency update move each tool to the newest version its
	// constraint allows (default true). A tool's own update: overrides it. Independent
	// of dependency.remediate, which governs the repo's own dependencies.
	Update *bool `yaml:"update,omitempty"`

	// MinReleaseAge is the release cooldown for toolchains, overriding
	// dependency.min_release_age; a tool's own min_release_age overrides it. Same forms
	// as dependency.min_release_age.
	MinReleaseAge *supplychain.Cooldown `yaml:"min_release_age,omitempty"`

	// Retention governs which installed versions of each tool are RETAINED in the
	// runner's shared cache (unset = engine default keep_last 2). Versions are ranked by
	// when any repo last USED them, so a version another repo still runs survives this
	// repo's prune. The policy is applied again to versions some repo declares exactly,
	// ranked by that pinned use, so an older pin is kept beside the newest release. This
	// repo's own pins are always protected — retention rotates residue nobody uses.
	Retention RetentionPolicy `yaml:"retention,omitempty"`
}

// ToolchainConfig is the want: map inside the toolchains section (tool name →
// desired version constraint).
type ToolchainConfig map[string]ToolConstraint

// UnmarshalYAML decodes the toolchains section — the wrapper shape ONLY (clean-break
// flag-day, no legacy acceptance). The retired flat tool→constraint shape fails with a
// targeted migration hint naming the offending key, never a bare unknown-field error.
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
		case "update":
			var b bool
			if err := node.Content[i+1].Decode(&b); err != nil {
				return fmt.Errorf("toolchains.update: must be true or false")
			}
			s.Update = &b
		case "min_release_age":
			var c supplychain.Cooldown
			if err := node.Content[i+1].Decode(&c); err != nil {
				return fmt.Errorf("toolchains.%w", err)
			}
			s.MinReleaseAge = &c
		default:
			return fmt.Errorf("toolchains.%s: toolchains: was a flat tool→constraint map; nest the tool map under toolchains.want: (update:, min_release_age: and retention: are the only other keys)", key)
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
	// Update overrides toolchains.update for this tool; nil inherits it.
	Update *bool `yaml:"update,omitempty"`
	// Variant picks the libc build ("gnu" or "musl") for a tool that publishes both;
	// empty uses the host's.
	Variant string `yaml:"variant,omitempty"`
	// SHA256 is the operator-asserted artifact digest per os/arch, for an exact version.
	// A declared digest holds the tool: the dependency update never rewrites it.
	SHA256 map[string]string `yaml:"sha256,omitempty"`
	// MinReleaseAge overrides toolchains.min_release_age for this tool; nil inherits it.
	MinReleaseAge *supplychain.Cooldown `yaml:"min_release_age,omitempty"`
}

// HoldReason reports why the dependency update must not move tool, or "" when it may: an
// asserted sha256 holds it, as does update: false.
func (s ToolchainSection) HoldReason(tool string) string {
	if c, ok := s.Want[tool]; ok && len(c.SHA256) > 0 {
		return "sha256 asserted: update the version and its digests together"
	}
	if !s.UpdateEnabled(tool) {
		return "toolchains update: false"
	}
	return ""
}

// UpdateEnabled reports whether the dependency update may move tool: its own update:,
// else the section's, else true.
func (s ToolchainSection) UpdateEnabled(tool string) bool {
	if c, ok := s.Want[tool]; ok && c.Update != nil {
		return *c.Update
	}
	if s.Update != nil {
		return *s.Update
	}
	return true
}

// UnmarshalYAML accepts two input forms and normalizes them to the internal Constraint —
// it answers only "what did the user write", performing no semantic validation:
//
//	go: 1.26.x                            # scalar shorthand
//	go: {version: 1.26.x}                 # explicit
//	go: {version: 1.26.x, update: false}  # explicit, held at its lock
//	cosign: {version: 3.0.6, sha256: {linux/amd64: <hex>}}  # exact, digest asserted
//	cargo-nextest: {version: 0.9.x, variant: musl}         # the musl build on any host
//	trivy: {version: 0.x, min_release_age: 0}              # adopt releases immediately
func (t *ToolConstraint) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := parseToolConstraint(node)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// parseToolConstraint is the shared normalization core: scalar-or-map → one
// ToolConstraint. The map form's keys are `version` (Cargo convention; its value is a
// version requirement), `update`, `variant`, `sha256` and `min_release_age`. A pre-lock inline `constraint`/`resolved` is
// silently ignored (node.Decode does not reject unknown keys) and superseded by the lock.
func parseToolConstraint(node *yaml.Node) (ToolConstraint, error) {
	var t ToolConstraint
	if node.Kind == yaml.ScalarNode {
		t.Constraint = node.Value
		return t, nil
	}
	var raw struct {
		Version       string                `yaml:"version"`
		Update        *bool                 `yaml:"update"`
		Variant       string                `yaml:"variant"`
		SHA256        yaml.Node             `yaml:"sha256"`
		MinReleaseAge *supplychain.Cooldown `yaml:"min_release_age"`
	}
	if err := node.Decode(&raw); err != nil {
		return t, err
	}
	t.Constraint = raw.Version
	t.Update = raw.Update
	t.Variant = raw.Variant
	t.MinReleaseAge = raw.MinReleaseAge
	switch raw.SHA256.Kind {
	case 0:
	case yaml.MappingNode:
		if err := raw.SHA256.Decode(&t.SHA256); err != nil {
			return t, fmt.Errorf("sha256: %w", err)
		}
	default:
		return t, fmt.Errorf("sha256: map each platform to its digest, e.g. sha256: {linux/amd64: <hex>}")
	}
	return t, nil
}
