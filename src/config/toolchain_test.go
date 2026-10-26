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

// TestToolchainSection_FlatShapeMigrationHint: the retired FLAT tool→constraint map
// fails with a targeted hint that names the offending key and the want: destination —
// never a bare unknown-field error. Clean break: no legacy acceptance.
func TestToolchainSection_FlatShapeMigrationHint(t *testing.T) {
	var s ToolchainSection
	err := yaml.Unmarshal([]byte("trivy: 0.69.3\ngrype: 0.110.x"), &s)
	if err == nil {
		t.Fatal("flat toolchains must be rejected — clean-break flag-day, no legacy shape")
	}
	msg := err.Error()
	if !strings.Contains(msg, "trivy") || !strings.Contains(msg, "toolchains.want") {
		t.Errorf("error must name the offending key and the want: destination, got %v", err)
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

// TestToolchainSection_Update: the section's update: and a tool's own update: cascade —
// the tool's wins, then the section's, then true.
func TestToolchainSection_Update(t *testing.T) {
	parse := func(y string) ToolchainSection {
		t.Helper()
		var s ToolchainSection
		if err := yaml.Unmarshal([]byte(y), &s); err != nil {
			t.Fatalf("decode %q: %v", y, err)
		}
		return s
	}

	unset := parse("want:\n  trivy: 0.x\n")
	if !unset.UpdateEnabled("trivy") || !unset.UpdateEnabled("not-declared") {
		t.Error("update must default to true")
	}

	off := parse("update: false\nwant:\n  trivy: 0.x\n  kubectl: {version: 1.34.x, update: true}\n  flux:\n    version: 2.8.x\n")
	if off.UpdateEnabled("trivy") {
		t.Error("section update: false must hold a tool with no override")
	}
	if !off.UpdateEnabled("kubectl") {
		t.Error("a tool's update: true must override the section's false")
	}
	if off.UpdateEnabled("flux") {
		t.Error("a block-form tool without update: must inherit the section")
	}
	if off.Want["kubectl"].Constraint != "1.34.x" {
		t.Errorf("flow map with update: must still yield its version, got %q", off.Want["kubectl"].Constraint)
	}

	perTool := parse("want:\n  cosign:\n    version: 3.0.6\n    update: false\n")
	if perTool.UpdateEnabled("cosign") {
		t.Error("a tool's update: false must hold it when the section is unset")
	}
}

func TestToolchainSection_UpdateMustBeBool(t *testing.T) {
	var s ToolchainSection
	err := yaml.Unmarshal([]byte("update: sometimes\nwant:\n  trivy: 0.x\n"), &s)
	if err == nil || !strings.Contains(err.Error(), "toolchains.update") {
		t.Errorf("a non-boolean update: must fail naming toolchains.update, got %v", err)
	}
}
