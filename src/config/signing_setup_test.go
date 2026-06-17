package config

import "testing"

func boolp(b bool) *bool { return &b }

func TestStateDir_Resolve(t *testing.T) {
	if p, err := (StateDir{Type: "volume", Name: "sig"}).Resolve(); err != nil || p != "/var/lib/stagefreight/sig" {
		t.Fatalf("volume: got %q, %v", p, err)
	}
	if p, _ := (StateDir{Type: "volume"}).Resolve(); p != "/var/lib/stagefreight/stagefreight-signing" {
		t.Errorf("default volume name: %q", p)
	}
	if p, err := (StateDir{Type: "host_path", Path: "/srv/sig"}).Resolve(); err != nil || p != "/srv/sig" {
		t.Fatalf("host_path: got %q, %v", p, err)
	}
	if _, err := (StateDir{Type: "host_path"}).Resolve(); err == nil {
		t.Error("host_path without path must error")
	}
	if _, err := (StateDir{Type: "s3"}).Resolve(); err == nil {
		t.Error("unknown type must error")
	}
	if p, err := (StateDir{}).Resolve(); err != nil || p != "" {
		t.Errorf("empty: got %q, %v", p, err)
	}
}

func TestSigningEnabled_DefaultsTrue(t *testing.T) {
	if !(SigningConfig{}).SigningEnabled() {
		t.Error("unset enabled must default to true")
	}
	if (SigningConfig{Enabled: boolp(false)}).SigningEnabled() {
		t.Error("enabled:false is the kill switch")
	}
}

func TestValidateSigningConfig(t *testing.T) {
	if errs := ValidateSigningConfig(SigningConfig{AutoProvision: true}); len(errs) == 0 {
		t.Error("auto_provision without a state_dir must error (no continuity)")
	}
	if errs := ValidateSigningConfig(SigningConfig{AutoProvision: true, StateDir: StateDir{Type: "volume"}}); len(errs) != 0 {
		t.Errorf("auto_provision + state_dir must be valid: %v", errs)
	}
	if errs := ValidateSigningConfig(SigningConfig{StateDir: StateDir{Type: "bad"}}); len(errs) == 0 {
		t.Error("invalid state_dir type must error")
	}
}
