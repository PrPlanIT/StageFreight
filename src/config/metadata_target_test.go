package config

import (
	"strings"
	"testing"
)

func hasErr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func TestValidateMetadataTarget(t *testing.T) {
	// A fully-populated metadata target validates cleanly.
	ok := validateTarget(TargetConfig{
		ID: "project-meta", Kind: "metadata",
		Registry:    StringOrList{"dockerhub", "harbor"},
		Repos:       StringOrList{"github-mirror", "primary"},
		Description: StringOrList{"a long tagline", "short"},
		Readme:      "README.md",
		Website:     "https://example.com",
		Topics:      []string{"ci-cd", "gitops"},
		Logo:        "logo.png",
	}, "publish[project-meta]", map[string]bool{}, nil)
	if len(ok) != 0 {
		t.Fatalf("valid metadata target produced errors: %v", ok)
	}

	// No destination at all is a config error.
	noDest := validateTarget(TargetConfig{ID: "m", Kind: "metadata"}, "publish[m]", map[string]bool{}, nil)
	if !hasErr(noDest, "requires at least one destination") {
		t.Fatalf("expected destination error, got: %v", noDest)
	}

	// registry-only and repos-only are both valid destinations.
	if errs := validateTarget(TargetConfig{ID: "m", Kind: "metadata", Registry: StringOrList{"dockerhub"}}, "p", map[string]bool{}, nil); len(errs) != 0 {
		t.Fatalf("registry-only metadata rejected: %v", errs)
	}
	if errs := validateTarget(TargetConfig{ID: "m", Kind: "metadata", Repos: StringOrList{"github-mirror"}}, "p", map[string]bool{}, nil); len(errs) != 0 {
		t.Fatalf("repos-only metadata rejected: %v", errs)
	}

	// metadata is not a build.
	withBuild := validateTarget(TargetConfig{ID: "m", Kind: "metadata", Registry: StringOrList{"dockerhub"}, Build: "x"}, "p", map[string]bool{}, nil)
	if !hasErr(withBuild, "does not use build") {
		t.Fatalf("expected build rejection, got: %v", withBuild)
	}
}

// TestMetadataTargetNotFanned guards the surfaced fan hazard: a metadata target with a
// multi-registry list must NOT be split (that would duplicate its repos: forge pushes).
func TestMetadataTargetNotFanned(t *testing.T) {
	out := expandMultiRegistryTargets(OrderedTargets{
		{ID: "m", Kind: "metadata", Registry: StringOrList{"dockerhub", "harbor"}},
	})
	if len(out) != 1 || len(out[0].Registry) != 2 {
		t.Fatalf("metadata must not fan; got %d targets, registry=%v", len(out), out[0].Registry)
	}

	// A normal registry target still fans, unchanged.
	fanned := expandMultiRegistryTargets(OrderedTargets{
		{ID: "img", Kind: "registry", Registry: StringOrList{"dockerhub", "harbor"}, Build: "b"},
	})
	if len(fanned) != 2 {
		t.Fatalf("registry target should fan into 2; got %d", len(fanned))
	}
}
