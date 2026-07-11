package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestToolConstraintParse: scalar shorthand and the explicit `version:` key both
// normalize to Constraint; a pre-lock inline sha256 is tolerated (ignored — it now lives
// in the lock).
func TestToolConstraintParse(t *testing.T) {
	cases := []struct {
		name       string
		yaml       string
		wantConstr string
	}{
		{"scalar shorthand", `go: 1.26.4`, "1.26.4"},
		{"explicit version", "go:\n  version: 1.26.4", "1.26.4"},
		{"wildcard version", "go:\n  version: 1.26.x", "1.26.x"},
		{"legacy inline sha256 ignored", "go:\n  version: 1.26.4\n  sha256: abc", "1.26.4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]ToolConstraint
			if err := yaml.Unmarshal([]byte(tc.yaml), &m); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := m["go"].Constraint; got != tc.wantConstr {
				t.Errorf("Constraint = %q, want %q", got, tc.wantConstr)
			}
		})
	}
}

// TestToolConstraintValidate: exact + well-formed wildcard pass; malformed grammar is
// rejected. The config is pure intent — no digest to validate here.
func TestToolConstraintValidate(t *testing.T) {
	valid := func(c ToolConstraint) error {
		cfg := &Config{Version: 1, Toolchains: ToolchainConfig{Desired: map[string]ToolConstraint{"trivy": c}}}
		_, err := Validate(cfg)
		return err
	}
	if err := valid(ToolConstraint{Constraint: "1.26.4"}); err != nil {
		t.Errorf("exact must validate, got %v", err)
	}
	if err := valid(ToolConstraint{Constraint: "1.26.x"}); err != nil {
		t.Errorf("wildcard must validate, got %v", err)
	}
	if err := valid(ToolConstraint{Constraint: "1.x.4"}); err == nil {
		t.Error("non-suffix-contiguous wildcard must be rejected")
	}
	if err := valid(ToolConstraint{Constraint: "1.26"}); err == nil {
		t.Error("bare partial must be rejected")
	}
}

// TestToolConstraintToolNameError: a parse error names the offending tool. `version`
// expects a scalar, so a mapping value is a decode error that must be wrapped with the
// tool name.
func TestToolConstraintToolNameError(t *testing.T) {
	var cfg struct {
		Toolchains ToolchainConfig `yaml:"toolchains"`
	}
	y := "toolchains:\n  desired:\n    helm:\n      version:\n        nested: bad"
	err := yaml.Unmarshal([]byte(y), &cfg)
	if err == nil || !strings.Contains(err.Error(), "helm") {
		t.Errorf("error must name the tool 'helm', got %v", err)
	}
}
