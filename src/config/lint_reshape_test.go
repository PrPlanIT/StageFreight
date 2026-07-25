package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLintModule_InlineOptions(t *testing.T) {
	var m ModuleConfig
	y := "enabled: true\ncache_ttl: 300\nsources:\n  go_modules: true"
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatalf("inline options should decode, got %v", err)
	}
	if m.Enabled == nil || !*m.Enabled {
		t.Fatal("enabled should be the typed field")
	}
	if m.Options["cache_ttl"] != 300 {
		t.Fatalf("inline key -> Options bag, got %v (%T)", m.Options["cache_ttl"], m.Options["cache_ttl"])
	}
	if _, ok := m.Options["sources"].(map[string]any); !ok {
		t.Fatalf("nested option preserved, got %T", m.Options["sources"])
	}
}

func TestLintModule_OptionsWrapperRejected(t *testing.T) {
	var m ModuleConfig
	err := yaml.Unmarshal([]byte("enabled: true\noptions:\n  cache_ttl: 300"), &m)
	if err == nil || !strings.Contains(err.Error(), "options:") {
		t.Fatalf("retired options: wrapper must be rejected loudly, got %v", err)
	}
}
