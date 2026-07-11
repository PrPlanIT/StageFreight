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

// TestToolConstraintValidate: exact constraints pass; wildcards are rejected until
// resolution is wired.
func TestToolConstraintValidate(t *testing.T) {
	exact := &Config{Version: 1, Toolchains: ToolchainConfig{Desired: map[string]ToolConstraint{"go": {Constraint: "1.26.4"}}}}
	if _, err := Validate(exact); err != nil {
		t.Errorf("exact constraint must validate, got %v", err)
	}

	wild := &Config{Version: 1, Toolchains: ToolchainConfig{Desired: map[string]ToolConstraint{"go": {Constraint: "1.26.x"}}}}
	_, err := Validate(wild)
	if err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("wildcard constraint must be rejected in slice 1, got %v", err)
	}
}
