package freshness

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// newConfiguredModule builds a freshnessModule and configures it via the real
// Configure/resolver.Config() plumbing (parseConfig → FreshnessConfig), exactly
// as the engine does for a real lint run. opts=nil applies production defaults.
func newConfiguredModule(t *testing.T, opts map[string]any) *freshnessModule {
	t.Helper()
	m := newModule()
	if err := m.Configure(opts); err != nil {
		t.Fatalf("Configure(%v) error: %v", opts, err)
	}
	return m
}

// (a) An outdated dependency renders a finding with the "current → latest
// available" message and a severity derived from the configured update-type
// mapping (default: minor → warning).
func TestDepsToFindingsOutdatedRendersCurrentArrowLatest(t *testing.T) {
	m := newConfiguredModule(t, nil)
	deps := []supplychain.Dependency{
		{
			Name:      "example.com/pkg",
			Current:   "v1.0.0",
			Latest:    "v1.2.0",
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
			Line:      5,
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	want := "example.com/pkg v1.0.0 → v1.2.0 available (2 minor behind)"
	if f.Message != want {
		t.Errorf("message = %q, want %q", f.Message, want)
	}
	if f.Severity != lint.SeverityWarning {
		t.Errorf("severity = %v, want warning (default minor severity)", f.Severity)
	}
	if f.Module != "freshness" || f.File != "go.mod" || f.Line != 5 {
		t.Errorf("module/file/line = %q/%q/%d, want freshness/go.mod/5", f.Module, f.File, f.Line)
	}
}

// (b) A dep with known vulnerabilities gets a "[N CVE(s)]" annotation appended
// to its message. severity_override is disabled here so the annotation is
// exercised independently of the escalation behavior covered by (c).
func TestDepsToFindingsAnnotatesCVECount(t *testing.T) {
	m := newConfiguredModule(t, map[string]any{
		"vulnerability": map[string]any{"severity_override": false},
	})
	deps := []supplychain.Dependency{
		{
			Name:      "vuln-pkg",
			Current:   "v1.0.0",
			Latest:    "v1.2.0",
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
			Vulnerabilities: []supplychain.VulnInfo{
				{ID: "CVE-2024-1"},
				{ID: "CVE-2024-2"},
			},
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if !strings.Contains(f.Message, "[2 CVE(s)]") {
		t.Errorf("message = %q, want it to contain [2 CVE(s)]", f.Message)
	}
	// severity_override is off, so the ordinary minor→warning mapping still
	// applies — the annotation must not itself escalate severity.
	if f.Severity != lint.SeverityWarning {
		t.Errorf("severity = %v, want warning (override disabled)", f.Severity)
	}
}

// (c) VulnSeverityOverride (default true) escalates an outdated+vulnerable dep
// to Critical, regardless of what the version-delta mapping alone would give.
func TestDepsToFindingsVulnSeverityOverrideEscalatesToCritical(t *testing.T) {
	m := newConfiguredModule(t, nil) // defaults: severity_override true
	deps := []supplychain.Dependency{
		{
			Name:      "vuln-pkg",
			Current:   "v1.0.0",
			Latest:    "v1.2.0", // minor bump → would be Warning without override
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
			Vulnerabilities: []supplychain.VulnInfo{
				{ID: "CVE-2024-1"},
			},
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Severity != lint.SeverityCritical {
		t.Errorf("severity = %v, want critical (severity_override escalation)", f.Severity)
	}
	if !strings.Contains(f.Message, "[1 CVE(s)]") {
		t.Errorf("message = %q, want it to still contain the CVE annotation", f.Message)
	}
}

// (d) A dependency within the configured tolerance and with no known CVEs is
// skipped entirely — no finding.
func TestDepsToFindingsWithinToleranceNoCVEsSkipped(t *testing.T) {
	m := newConfiguredModule(t, nil) // default patch_tolerance: 1
	deps := []supplychain.Dependency{
		{
			Name:      "patched-pkg",
			Current:   "v1.0.0",
			Latest:    "v1.0.1", // 1 patch behind, within default tolerance of 1
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none (within tolerance, no CVEs)", findings)
	}
}

// (e) IsIgnored: a dependency matching an ignore glob is skipped even though it
// is badly outdated.
func TestDepsToFindingsIgnoredByGlob(t *testing.T) {
	m := newConfiguredModule(t, map[string]any{
		"ignore": []string{"ignored-*"},
	})
	deps := []supplychain.Dependency{
		{
			Name:      "ignored-pkg",
			Current:   "v1.0.0",
			Latest:    "v2.0.0", // major bump — would normally be Critical
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none (ignored by glob)", findings)
	}
}

// (e) SourceEnabled: a dependency whose ecosystem is disabled via
// sources.go_modules=false is skipped even though it is outdated.
func TestDepsToFindingsSourceDisabled(t *testing.T) {
	m := newConfiguredModule(t, map[string]any{
		"sources": map[string]any{"go_modules": false},
	})
	deps := []supplychain.Dependency{
		{
			Name:      "example.com/pkg",
			Current:   "v1.0.0",
			Latest:    "v2.0.0",
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
		},
	}

	findings := m.depsToFindings(deps)
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none (gomod source disabled)", findings)
	}
}
