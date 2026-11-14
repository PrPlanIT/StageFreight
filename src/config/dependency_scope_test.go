package config

import (
	"strings"
	"testing"
)

// source_images gates discovery, so the "all" filter is unchanged by it and a narrowed
// filter lists it only when enabled.
func TestScopeToEcosystems_SourceImages(t *testing.T) {
	cases := []struct {
		scope DependencyScopeConfig
		want  string
	}{
		{DependencyScopeConfig{GoModules: true, DockerfileEnv: true}, ""},
		{DependencyScopeConfig{GoModules: true, DockerfileEnv: true, SourceImages: true}, ""},
		{DependencyScopeConfig{GoModules: true}, "gomod"},
		{DependencyScopeConfig{GoModules: true, SourceImages: true}, "gomod,source-image"},
		{DependencyScopeConfig{DockerfileEnv: true, SourceImages: true}, "docker-image,github-release,source-image"},
		{DependencyScopeConfig{SourceImages: true}, "source-image"},
	}
	for _, c := range cases {
		if got := strings.Join(c.scope.ScopeToEcosystems(), ","); got != c.want {
			t.Errorf("%+v: ScopeToEcosystems = %q, want %q", c.scope, got, c.want)
		}
	}
}

func TestPropagateSourceImages(t *testing.T) {
	cfg := &Config{}
	cfg.Dependency.Scope.SourceImages = true
	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Lint.Modules["freshness"].Options["source_images"]; got != true {
		t.Errorf("freshness source_images = %v, want true", got)
	}

	off := &Config{}
	if err := Normalize(off); err != nil {
		t.Fatal(err)
	}
	if _, set := off.Lint.Modules["freshness"].Options["source_images"]; set {
		t.Error("source_images off must not reach the freshness options")
	}
}

func TestSourceImagesFreshnessOptionRejected(t *testing.T) {
	cfg := &Config{}
	cfg.Lint.Modules = map[string]ModuleConfig{"freshness": {Options: map[string]any{"source_images": true}}}
	err := Normalize(cfg)
	if err == nil || !strings.Contains(err.Error(), "dependency.scope.source_images") {
		t.Errorf("want the scope error, got %v", err)
	}
}

func TestValidate_SourceImagesCountsAsScope(t *testing.T) {
	const msg = "at least one scope must be true"
	errStr := func(scope DependencyScopeConfig) string {
		cfg := &Config{Version: 1}
		cfg.Dependency.Enabled = true
		cfg.Dependency.Scope = scope
		if _, err := Validate(cfg); err != nil {
			return err.Error()
		}
		return ""
	}
	if strings.Contains(errStr(DependencyScopeConfig{SourceImages: true}), msg) {
		t.Error("source_images alone must satisfy the scope requirement")
	}
	if !strings.Contains(errStr(DependencyScopeConfig{}), msg) {
		t.Error("no scope enabled must still be refused")
	}
}
