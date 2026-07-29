package config

import "testing"

// Typed refs split into build vs scribe id spaces; a bare id is a build (back-compat).
func TestDependsOn_TypedRefs(t *testing.T) {
	b := BuildConfig{ID: "site", DependsOn: StringOrList{
		"docgen", "build:compile", "scribe:ref-pages", "scribe:inventory",
	}}

	bd := b.BuildDeps()
	if len(bd) != 2 || bd[0] != "docgen" || bd[1] != "compile" {
		t.Fatalf("BuildDeps = %v, want [docgen compile] (bare + build: stripped)", bd)
	}
	sd := b.ScribeDeps()
	if len(sd) != 2 || sd[0] != "ref-pages" || sd[1] != "inventory" {
		t.Fatalf("ScribeDeps = %v, want [ref-pages inventory]", sd)
	}
}

// A bare scalar depends_on stays a build ref — existing configs are unchanged.
func TestDependsOn_BareIsBuild(t *testing.T) {
	b := BuildConfig{ID: "x", DependsOn: StringOrList{"y"}}
	if bd := b.BuildDeps(); len(bd) != 1 || bd[0] != "y" {
		t.Fatalf("bare dep must be a build ref, got %v", bd)
	}
	if sd := b.ScribeDeps(); len(sd) != 0 {
		t.Fatalf("bare dep must not be a scribe ref, got %v", sd)
	}
}
