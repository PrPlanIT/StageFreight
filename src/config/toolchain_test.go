package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestToolConstraintParse: the three input forms all normalize to Constraint,
// and constraint+version together is a structural error.
func TestToolConstraintParse(t *testing.T) {
	cases := []struct {
		name       string
		yaml       string
		wantConstr string
		wantSHA    string
		wantErr    bool
	}{
		{"scalar shorthand", `go: 1.26.4`, "1.26.4", "", false},
		{"explicit constraint", "go:\n  constraint: 1.26.4", "1.26.4", "", false},
		{"legacy version alias", "go:\n  version: 1.26.4", "1.26.4", "", false},
		{"constraint + sha256", "go:\n  constraint: 1.26.4\n  sha256: abc", "1.26.4", "abc", false},
		{"both constraint and version", "go:\n  constraint: 1.26.4\n  version: 1.26.5", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]ToolConstraint
			err := yaml.Unmarshal([]byte(tc.yaml), &m)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got none", tc.yaml)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := m["go"].Constraint; got != tc.wantConstr {
				t.Errorf("Constraint = %q, want %q", got, tc.wantConstr)
			}
			if got := m["go"].SHA256; got != tc.wantSHA {
				t.Errorf("SHA256 = %q, want %q", got, tc.wantSHA)
			}
		})
	}
}

// TestToolConstraintValidate: exact + well-formed wildcard pass; malformed grammar
// and wildcard-plus-sha256 are rejected.
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
		t.Errorf("wildcard must validate now, got %v", err)
	}
	if err := valid(ToolConstraint{Constraint: "1.x.4"}); err == nil {
		t.Error("non-suffix-contiguous wildcard must be rejected")
	}
	if err := valid(ToolConstraint{Constraint: "1.26"}); err == nil {
		t.Error("bare partial must be rejected")
	}
	if err := valid(ToolConstraint{Constraint: "1.26.x", SHA256: "abc"}); err == nil {
		t.Error("wildcard + sha256 must be rejected")
	}
	if err := valid(ToolConstraint{Constraint: "1.26.4", SHA256: "abc"}); err != nil {
		t.Errorf("exact + sha256 must validate, got %v", err)
	}
}

// TestToolConstraintToolNameError: the both-keys error names the offending tool.
func TestToolConstraintToolNameError(t *testing.T) {
	var cfg struct {
		Toolchains ToolchainConfig `yaml:"toolchains"`
	}
	y := "toolchains:\n  desired:\n    helm:\n      constraint: 1.26.4\n      version: 1.26.5"
	err := yaml.Unmarshal([]byte(y), &cfg)
	if err == nil || !strings.Contains(err.Error(), "helm") {
		t.Errorf("error must name the tool 'helm', got %v", err)
	}
}

func TestEffectiveVersion(t *testing.T) {
	cases := []struct{ constraint, resolved, want string }{
		{"1.26.4", "", "1.26.4"},       // exact → the constraint itself
		{"1.26.x", "1.26.7", "1.26.7"}, // wildcard + lock → resolved
		{"1.26.x", "", ""},             // wildcard, no lock → empty (caller falls back to default)
	}
	for _, c := range cases {
		if got := (ToolConstraint{Constraint: c.constraint, Resolved: c.resolved}).EffectiveVersion(); got != c.want {
			t.Errorf("EffectiveVersion(%q,%q) = %q, want %q", c.constraint, c.resolved, got, c.want)
		}
	}
}

func TestValidate_WildcardSha256RequiresResolved(t *testing.T) {
	mk := func(c ToolConstraint) error {
		_, err := Validate(&Config{Version: 1, Toolchains: ToolchainConfig{Desired: map[string]ToolConstraint{"trivy": c}}})
		return err
	}
	if err := mk(ToolConstraint{Constraint: "1.26.x", SHA256: "abc"}); err == nil {
		t.Error("wildcard + sha256 WITHOUT resolved must be rejected")
	}
	if err := mk(ToolConstraint{Constraint: "1.26.x", Resolved: "1.26.7", SHA256: "abc"}); err != nil {
		t.Errorf("wildcard + resolved + sha256 (the lock) must validate, got %v", err)
	}
	if err := mk(ToolConstraint{Constraint: "1.26.4", SHA256: "abc"}); err != nil {
		t.Errorf("exact + sha256 must validate, got %v", err)
	}
}
