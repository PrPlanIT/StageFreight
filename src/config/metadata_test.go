package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMetadata_Decode covers a full metadata block: org/title/names/topics/license/
// labels plus a SCOPED description (tiered default + a single-string named override).
func TestMetadata_Decode(t *testing.T) {
	data := `
org: HomeLabHD
title: "ARK Server"
names: { dockerhub: arkserver }
description:
  default:
    - "short"
    - "long"
  dockerhub: "docker blurb"
topics: [ark, game-server]
license: MIT
labels: { funding: "https://example.com/sponsor" }
`
	var m MetadataConfig
	if err := yaml.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Org != "HomeLabHD" || m.Title != "ARK Server" {
		t.Errorf("org/title = %q/%q", m.Org, m.Title)
	}
	if m.Names["dockerhub"] != "arkserver" {
		t.Errorf("names = %v", m.Names)
	}
	if got := m.Description.Default; len(got) != 2 || got[0] != "short" || got[1] != "long" {
		t.Errorf("description default = %v, want tiered [short long]", got)
	}
	if got := m.Description.For("dockerhub").First(); got != "docker blurb" {
		t.Errorf("description[dockerhub] = %q, want %q", got, "docker blurb")
	}
	// Un-named surface falls to the default; First() is the shortest tier.
	if got := m.Description.For("github").First(); got != "short" {
		t.Errorf("description[github] = %q, want default shortest %q", got, "short")
	}
	if m.License != "MIT" || m.Labels["funding"] != "https://example.com/sponsor" {
		t.Errorf("license/labels = %q / %v", m.License, m.Labels)
	}
}

// TestMetadata_PlainDescription: a bare value is the default, no BySurface.
func TestMetadata_PlainDescription(t *testing.T) {
	var m MetadataConfig
	if err := yaml.Unmarshal([]byte(`description: "one line"`), &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Description.Default.First(); got != "one line" {
		t.Errorf("default = %q", got)
	}
	if m.Description.BySurface != nil {
		t.Errorf("BySurface should be nil for a plain value, got %v", m.Description.BySurface)
	}
}

// TestScoped_MissingDefault: a scoped map without `default` is rejected.
func TestScoped_MissingDefault(t *testing.T) {
	var m MetadataConfig
	if err := yaml.Unmarshal([]byte(`description: { dockerhub: x }`), &m); err == nil {
		t.Fatal("expected error for scoped description with no default")
	}
}

// TestReadme_Scoped: readme is a scoped scalar (default path or per-surface paths).
func TestReadme_Scoped(t *testing.T) {
	var m MetadataConfig
	if err := yaml.Unmarshal([]byte("readme: { default: README.md, dockerhub: README.docker.md }"), &m); err != nil {
		t.Fatal(err)
	}
	if m.Readme.Default != "README.md" {
		t.Errorf("readme default = %q", m.Readme.Default)
	}
	if got := m.Readme.For("dockerhub"); got != "README.docker.md" {
		t.Errorf("readme[dockerhub] = %q", got)
	}
	if got := m.Readme.For("github"); got != "README.md" {
		t.Errorf("readme[github] = %q, want default", got)
	}
}
