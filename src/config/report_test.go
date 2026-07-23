package config

import "testing"

// TestLoadWithReportReturnsEntries guards the contract that LoadWithReport exposes
// the same per-value merge entries it built the section provenance from — one
// resolution, entries returned (not a second resolve). `config resolve --verbose`
// renders from these.
func TestLoadWithReportReturnsEntries(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nsecurity:\n  preset: ./sec.yml\n  output: LOCAL_WINS\n",
		"sec.yml", "security:\n  output: FROM_PRESET\n  sbom: true\n")

	_, report, entries, err := LoadWithReport(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected per-value entries for a preset config, got none")
	}

	// The report's section provenance and the entries must agree: security came
	// from a preset, and the local sibling counts as one override.
	if len(report.Presets) != 1 || report.Presets[0] != "./sec.yml" {
		t.Fatalf("report.Presets = %v, want [./sec.yml]", report.Presets)
	}
	if report.Overrides != 1 {
		t.Fatalf("report.Overrides = %d, want 1", report.Overrides)
	}

	var sawOverriddenOutput bool
	for _, e := range entries {
		if e.Path == "security.output" && e.Overridden {
			sawOverriddenOutput = true
		}
	}
	if !sawOverriddenOutput {
		t.Fatal("expected a security.output entry marked overridden by the local sibling")
	}
}

// TestLoadWithReportPresetFreeNoEntries confirms a preset-free config resolves via
// the original-bytes path (no map round-trip): no preset entries, sections still
// derived, status ok.
func TestLoadWithReportPresetFreeNoEntries(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nsecurity:\n  output: PLAIN\n", "", "")

	_, report, _, err := LoadWithReport(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(report.Presets) != 0 {
		t.Fatalf("preset-free config reported presets: %v", report.Presets)
	}
	if report.Status != "ok" {
		t.Fatalf("status = %q, want ok", report.Status)
	}
	var sawSecurity bool
	for _, s := range report.Sections {
		if s.Name == "security" && s.Active {
			sawSecurity = true
		}
	}
	if !sawSecurity {
		t.Fatal("security section not active for a preset-free config that defines it")
	}
}
