package config

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestTargetConfig_GenericPackageFieldsRoundTrip pins the generic-package schema
// fields (repo/package/version) as recognized by the STRICT (KnownFields) decoder
// config.Load uses, and confirms they survive a YAML round-trip. version is the
// immutable package version — deliberately NOT tag (a git ref): a package has a
// version, a release has a tag.
func TestTargetConfig_GenericPackageFieldsRoundTrip(t *testing.T) {
	const y = `
id: pkg-dev
kind: generic-package
repo: primary
package: stagefreight
archives: bin-dev
version: "dev-{sha:8}"
aliases: ["latest-dev"]
`
	var got TargetConfig
	dec := yaml.NewDecoder(bytes.NewReader([]byte(y)))
	dec.KnownFields(true) // mirror config.Load strictness — unknown fields must error
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("strict decode of generic-package target: %v", err)
	}
	if got.Repo != "primary" || got.Package != "stagefreight" || got.Version != "dev-{sha:8}" {
		t.Errorf("fields = repo=%q package=%q version=%q", got.Repo, got.Package, got.Version)
	}
	if got.Archives != "bin-dev" {
		t.Errorf("Archives = %q, want bin-dev", got.Archives)
	}

	out, err := yaml.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(out, []byte("dev-{sha:8}")) || !bytes.Contains(out, []byte("repo: primary")) {
		t.Errorf("round-trip lost generic-package fields:\n%s", out)
	}
}

// TestValidateTarget_GenericPackage covers the kind-specific validation rules,
// most importantly that version is mandatory — alias-only publication is illegal
// because every mutable alias must have an immutable version behind it.
func TestValidateTarget_GenericPackage(t *testing.T) {
	base := func() TargetConfig {
		return TargetConfig{
			ID:       "pkg",
			Kind:     "generic-package",
			Repo:     "primary",
			Archives: "bin-dev",
			Version:  "dev-{sha:8}",
			Aliases:  []string{"latest-dev"},
		}
	}
	check := func(name string, mutate func(*TargetConfig), wantSubstr string) {
		t.Run(name, func(t *testing.T) {
			tc := base()
			mutate(&tc)
			errs := validateTarget(tc, "targets[pkg]", map[string]bool{}, nil)
			joined := strings.Join(errs, "; ")
			if wantSubstr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got: %s", joined)
				}
				return
			}
			if !strings.Contains(joined, wantSubstr) {
				t.Fatalf("expected error containing %q, got: %s", wantSubstr, joined)
			}
		})
	}

	check("valid", func(tc *TargetConfig) {}, "")
	check("alias-only rejected (no version)", func(tc *TargetConfig) { tc.Version = "" }, "requires version")
	check("missing repo", func(tc *TargetConfig) { tc.Repo = "" }, "requires repo")
	check("missing archives", func(tc *TargetConfig) { tc.Archives = "" }, "requires archives")
	check("tag not allowed (use version)", func(tc *TargetConfig) { tc.Tag = "v{version}" }, "tag is not valid")
	check("inline forge fields rejected", func(tc *TargetConfig) { tc.Provider = "gitlab" }, "not valid for kind generic-package")
}

// TestValidateTargetRepoRefs_GenericPackage confirms a generic-package repo: must
// resolve to a declared repos[] entry.
func TestValidateTargetRepoRefs_GenericPackage(t *testing.T) {
	repos := []RepoConfig{{ID: "primary"}}

	bad := []TargetConfig{{ID: "pkg", Kind: "generic-package", Repo: "ghost"}}
	if errs := ValidateTargetRepoRefs(bad, repos); len(errs) == 0 ||
		!strings.Contains(strings.Join(errs, "; "), "not found in repos") {
		t.Fatalf("expected repo-not-found error, got: %v", errs)
	}

	good := []TargetConfig{{ID: "pkg", Kind: "generic-package", Repo: "primary"}}
	if errs := ValidateTargetRepoRefs(good, repos); len(errs) != 0 {
		t.Fatalf("expected no errors for valid repo, got: %v", errs)
	}
}
