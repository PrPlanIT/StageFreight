package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// security_summary is a yes/no toggle on the release TARGET — it says what that release
// carries, like notes and limits, so each channel can differ. The retired path-string
// form no longer type-checks against a bool.
func TestTargetSecuritySummary_BoolShape(t *testing.T) {
	var tc TargetConfig
	if err := yaml.Unmarshal([]byte("kind: release\nsecurity_summary: true"), &tc); err != nil {
		t.Fatalf("bool shape should decode, got %v", err)
	}
	if !tc.SecuritySummary {
		t.Fatal("security_summary: true should set the toggle")
	}
}

func TestTargetSecuritySummary_PathShapeRejected(t *testing.T) {
	var tc TargetConfig
	if err := yaml.Unmarshal([]byte("kind: release\nsecurity_summary: \".stagefreight/security\""), &tc); err == nil {
		t.Fatal("retired path-string security_summary must fail to decode as bool")
	}
}

// The move off the subsystem block must be a clean break, not a silent one: a config
// still declaring these under release: has to fail loudly. Decoding is strict, so the
// retired keys surface as unknown fields rather than being quietly dropped — a repo that
// silently stopped attaching its security summary would look like a scanner regression.
func TestReleaseContentTogglesRetiredFromSubsystemBlock(t *testing.T) {
	for _, key := range []string{"security_summary", "registry_links", "catalog_links"} {
		var r ReleaseConfig
		dec := yaml.NewDecoder(strings.NewReader("enabled: true\n" + key + ": true"))
		dec.KnownFields(true)
		if err := dec.Decode(&r); err == nil {
			t.Errorf("release.%s must no longer decode — it moved to the release target", key)
		}
	}
}
