package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTestSuites_KeyedMapDecodes(t *testing.T) {
	var tc TestConfig
	in := "enabled: true\nsuites:\n  unit:\n    tool: go\n    packages: [./...]\n"
	if err := yaml.Unmarshal([]byte(in), &tc); err != nil {
		t.Fatalf("keyed-map suites should decode, got %v", err)
	}
	if len(tc.Suites) != 1 || tc.Suites[0].ID != "unit" {
		t.Fatalf("key should become the suite ID, got %+v", tc.Suites)
	}
	if tc.Suites[0].Tool != "go" {
		t.Fatalf("suite fields should decode, got tool=%q", tc.Suites[0].Tool)
	}
}

func TestTestSuites_ListShapeRejected(t *testing.T) {
	var tc TestConfig
	in := "enabled: true\nsuites:\n  - id: unit\n    tool: go\n"
	err := yaml.Unmarshal([]byte(in), &tc)
	if err == nil {
		t.Fatal("retired list shape must be rejected at decode")
	}
	if !strings.Contains(err.Error(), "id → entry map") {
		t.Fatalf("expected the keyed-map rejection error, got %v", err)
	}
}
