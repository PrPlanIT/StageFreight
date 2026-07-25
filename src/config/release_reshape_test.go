package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseSecuritySummary_BoolShape(t *testing.T) {
	var r ReleaseConfig
	if err := yaml.Unmarshal([]byte("enabled: true\nsecurity_summary: true"), &r); err != nil {
		t.Fatalf("bool shape should decode, got %v", err)
	}
	if !r.SecuritySummary {
		t.Fatal("security_summary: true should set the toggle")
	}
}

func TestReleaseSecuritySummary_PathShapeRejected(t *testing.T) {
	// The retired path-string form no longer type-checks against a bool.
	var r ReleaseConfig
	if err := yaml.Unmarshal([]byte("security_summary: \".stagefreight/security\""), &r); err == nil {
		t.Fatal("retired path-string security_summary must fail to decode as bool")
	}
}
