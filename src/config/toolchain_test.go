package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestToolConstraintParse: scalar shorthand and the explicit `version:` key both
// normalize to Constraint.
func TestToolConstraintParse(t *testing.T) {
	cases := []struct {
		name       string
		yaml       string
		wantConstr string
	}{
		{"scalar shorthand", `go: 1.26.4`, "1.26.4"},
		{"explicit version", "go:\n  version: 1.26.4", "1.26.4"},
		{"wildcard version", "go:\n  version: 1.26.x", "1.26.x"},
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

// TestToolConstraint_SHA256: a platform-keyed sha256 map parses; a scalar sha256 fails
// with a hint to key it by platform.
func TestToolConstraint_SHA256(t *testing.T) {
	var m map[string]ToolConstraint
	y := "cosign:\n  version: 3.0.6\n  sha256:\n    linux/amd64: " + strings.Repeat("a", 64) + "\n    linux/arm64: " + strings.Repeat("b", 64) + "\n"
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := m["cosign"].SHA256["linux/arm64"]; got != strings.Repeat("b", 64) {
		t.Errorf("arm64 digest = %q", got)
	}

	err := yaml.Unmarshal([]byte("cosign:\n  version: 3.0.6\n  sha256: abc\n"), &m)
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Errorf("scalar sha256 must fail with a platform hint, got %v", err)
	}
}

func TestToolConstraint_SHA256Validate(t *testing.T) {
	hex := strings.Repeat("c", 64)
	validate := func(c ToolConstraint) error {
		_, err := Validate(&Config{Version: 1, Toolchains: ToolchainSection{Want: ToolchainConfig{"cosign": c}}})
		return err
	}
	if err := validate(ToolConstraint{Constraint: "3.0.6", SHA256: map[string]string{"linux/amd64": hex}}); err != nil {
		t.Errorf("exact version + well-formed digest must validate, got %v", err)
	}
	cases := map[string]ToolConstraint{
		"wildcard with digest": {Constraint: "3.x", SHA256: map[string]string{"linux/amd64": hex}},
		"bad platform key":     {Constraint: "3.0.6", SHA256: map[string]string{"amd64": hex}},
		"short digest":         {Constraint: "3.0.6", SHA256: map[string]string{"linux/amd64": "abc"}},
		"uppercase digest":     {Constraint: "3.0.6", SHA256: map[string]string{"linux/amd64": strings.ToUpper(hex)}},
	}
	for name, c := range cases {
		if err := validate(c); err == nil || !strings.Contains(err.Error(), "toolchains.want.cosign.sha256") {
			t.Errorf("%s: must fail naming toolchains.want.cosign.sha256, got %v", name, err)
		}
	}
}

// TestToolchainSection_HoldReason: an asserted digest holds a tool even when update is on;
// update: false holds it too; otherwise it may move.
func TestToolchainSection_HoldReason(t *testing.T) {
	on, off := true, false
	s := ToolchainSection{Update: &on, Want: ToolchainConfig{
		"cosign":  {Constraint: "3.0.6", SHA256: map[string]string{"linux/amd64": strings.Repeat("d", 64)}},
		"kubectl": {Constraint: "1.34.2", Update: &off},
		"trivy":   {Constraint: "0.x"},
	}}
	if r := s.HoldReason("cosign"); !strings.Contains(r, "sha256") {
		t.Errorf("asserted digest must hold cosign, got %q", r)
	}
	if r := s.HoldReason("kubectl"); !strings.Contains(r, "update: false") {
		t.Errorf("update: false must hold kubectl, got %q", r)
	}
	if r := s.HoldReason("trivy"); r != "" {
		t.Errorf("trivy must be movable, got %q", r)
	}
}

func TestToolConstraint_Variant(t *testing.T) {
	var m map[string]ToolConstraint
	if err := yaml.Unmarshal([]byte("cargo-nextest: {version: 0.9.x, variant: musl}"), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["cargo-nextest"].Variant != "musl" || m["cargo-nextest"].Constraint != "0.9.x" {
		t.Errorf("got %+v", m["cargo-nextest"])
	}
	validate := func(c ToolConstraint) error {
		_, err := Validate(&Config{Version: 1, Toolchains: ToolchainSection{Want: ToolchainConfig{"cargo-nextest": c}}})
		return err
	}
	if err := validate(ToolConstraint{Constraint: "0.9.x", Variant: "gnu"}); err != nil {
		t.Errorf("gnu must validate, got %v", err)
	}
	if err := validate(ToolConstraint{Constraint: "0.9.x", Variant: "glibc"}); err == nil || !strings.Contains(err.Error(), "must be gnu or musl") {
		t.Errorf("an unknown variant must fail, got %v", err)
	}
	hex := strings.Repeat("e", 64)
	if err := validate(ToolConstraint{Constraint: "0.9.1", Variant: "musl", SHA256: map[string]string{"linux/amd64-musl": hex}}); err != nil {
		t.Errorf("a -musl digest key must validate, got %v", err)
	}
	if err := validate(ToolConstraint{Constraint: "0.9.1", SHA256: map[string]string{"linux/amd64-glibc": hex}}); err == nil {
		t.Error("an unknown key suffix must fail")
	}
}
