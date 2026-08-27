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
		cfg := &Config{Version: 1, Toolchains: ToolchainSection{Want: ToolchainConfig{"trivy": c}}}
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

// TestToolchainSection_WrapperShape: the toolchains block is the want: map plus an
// optional retention: policy; scalar and {version: …} tool forms both resolve.
func TestToolchainSection_WrapperShape(t *testing.T) {
	var s ToolchainSection
	y := "want:\n  trivy: 0.69.3\n  grype: \"0.110.x\"\n  cosign: {version: 3.0.6}\nretention:\n  keep_last: 2"
	if err := yaml.Unmarshal([]byte(y), &s); err != nil {
		t.Fatalf("wrapper toolchains should decode, got %v", err)
	}
	if s.Want["trivy"].Constraint != "0.69.3" || s.Want["grype"].Constraint != "0.110.x" || s.Want["cosign"].Constraint != "3.0.6" {
		t.Fatalf("exact/wildcard/map forms should all resolve, got %+v", s.Want)
	}
	if s.Retention.KeepLast != 2 {
		t.Fatalf("retention.keep_last should decode, got %+v", s.Retention)
	}
	// The wildcard is a valid constraint (grammar unchanged by the reshape).
	cfg := &Config{Version: 1, Toolchains: ToolchainSection{Want: ToolchainConfig{"grype": {Constraint: "0.110.x"}}}}
	if _, err := Validate(cfg); err != nil {
		t.Fatalf("wildcard constraint must validate, got %v", err)
	}
}

// TestToolchainSection_FlatShapeDualAccept: the retired FLAT tool→constraint map is
// dual-accepted for the transition (the deployed image must be able to parse SOME
// shape while the wrapper-aware image builds): it decodes into Want and is flagged
// LegacyFlat. A wrapper mixing an unknown key still errors with the migration hint.
func TestToolchainSection_FlatShapeDualAccept(t *testing.T) {
	var s ToolchainSection
	if err := yaml.Unmarshal([]byte("trivy: 0.69.3\ngrype: 0.110.x"), &s); err != nil {
		t.Fatalf("flat toolchains must dual-accept during the transition, got %v", err)
	}
	if s.Want["trivy"].Constraint != "0.69.3" || !s.LegacyFlat() {
		t.Fatalf("flat shape must decode into Want and flag LegacyFlat, got %+v", s)
	}
	var w ToolchainSection
	err := yaml.Unmarshal([]byte("want:\n  trivy: 0.69.3\nbogus: 1"), &w)
	if err == nil || !strings.Contains(err.Error(), "toolchains.want") {
		t.Errorf("wrapper with unknown key must error with the migration hint, got %v", err)
	}
}

// TestToolConstraintToolNameError: a parse error names the offending tool. `version`
// expects a scalar, so a mapping value is a decode error that must be wrapped with the
// tool name.
func TestToolConstraintToolNameError(t *testing.T) {
	var cfg struct {
		Toolchains ToolchainSection `yaml:"toolchains"`
	}
	y := "toolchains:\n  want:\n    helm:\n      version:\n        nested: bad"
	err := yaml.Unmarshal([]byte(y), &cfg)
	if err == nil || !strings.Contains(err.Error(), "helm") {
		t.Errorf("error must name the tool 'helm', got %v", err)
	}
}
