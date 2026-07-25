package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildCacheExternal_RegistryShape(t *testing.T) {
	var ext ExternalCacheConfig
	if err := yaml.Unmarshal([]byte("registry: harbor\npath: cache\nfallback: main"), &ext); err != nil {
		t.Fatalf("registry shape should decode, got %v", err)
	}
	if ext.Registry != "harbor" || ext.Path != "cache" {
		t.Fatalf("registry ref should resolve, got %+v", ext)
	}
}

func TestBuildCacheExternal_TargetFieldGone(t *testing.T) {
	// The retired `target:` field is rejected by the loader's strict decode (the same
	// KnownFields(true) loadResolved uses) — no silent acceptance.
	var ext ExternalCacheConfig
	dec := yaml.NewDecoder(strings.NewReader("target: harbor-dev"))
	dec.KnownFields(true)
	if err := dec.Decode(&ext); err == nil {
		t.Fatal("retired external.target field must be rejected under KnownFields")
	}
}
