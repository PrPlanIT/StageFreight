package vulnerabilities

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
)

// TestPersistCatalogue: the source Assessment is written to
// .stagefreight/security/source-vulns.json under the scanned root, and parses
// back — the cross-phase catalogue the review phase consumes.
func TestPersistCatalogue(t *testing.T) {
	dir := t.TempDir()
	files := []lint.FileInfo{{Path: "go.mod", AbsPath: filepath.Join(dir, "go.mod")}}
	vulns := []analysis.Vulnerability{{
		ID: "GO-2026-5932", Severity: "MODERATE", Verdict: analysis.VerdictInfo,
		Packages: []string{"golang.org/x/crypto@v0.54.0"}, File: "go.mod", Line: 17,
		Surfaces: []analysis.Surface{analysis.SurfaceSource},
	}}

	newModule().persistCatalogue(files, vulns)

	data, err := os.ReadFile(filepath.Join(dir, ".stagefreight", "security", "source-vulns.json"))
	if err != nil {
		t.Fatalf("catalogue not written: %v", err)
	}
	sa, err := analysis.UnmarshalSourceAssessment(data)
	if err != nil {
		t.Fatalf("catalogue not valid JSON: %v", err)
	}
	if len(sa.Vulnerabilities) != 1 || sa.Vulnerabilities[0].ID != "GO-2026-5932" {
		t.Fatalf("round-trip lost the vuln: %+v", sa.Vulnerabilities)
	}
	if got := sa.Vulnerabilities[0].Surfaces; len(got) != 1 || got[0] != "source" {
		t.Errorf("surfaces = %v, want [source]", got)
	}
}

// TestPersistCatalogueSkipsEmpty: no vulnerabilities → no file (additive, no noise).
func TestPersistCatalogueSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	files := []lint.FileInfo{{Path: "go.mod", AbsPath: filepath.Join(dir, "go.mod")}}
	newModule().persistCatalogue(files, nil)
	if _, err := os.Stat(filepath.Join(dir, ".stagefreight", "security", "source-vulns.json")); !os.IsNotExist(err) {
		t.Error("empty vulns should not write a catalogue file")
	}
}

// TestToFindingRendersPackageVersion (fix #6): the rendered message includes the
// affected package@version (as the former osv `pkg@version` and freshness
// `name@current` messages did), plus the advisory id, summary, and fixed-in.
func TestToFindingRendersPackageVersion(t *testing.T) {
	v := analysis.Vulnerability{
		ID:       "GHSA-xxxx",
		Summary:  "buffer overflow",
		FixedIn:  "0.40.0",
		Packages: []string{"golang.org/x/net@0.38.0"},
		File:     "go.mod",
		Line:     7,
		Verdict:  analysis.VerdictCritical,
	}
	f := toFinding(v)
	want := "GHSA-xxxx: buffer overflow (golang.org/x/net@0.38.0, fixed in 0.40.0)"
	if f.Message != want {
		t.Errorf("message = %q, want %q", f.Message, want)
	}
	if f.Severity != lint.SeverityCritical {
		t.Errorf("severity = %v, want critical", f.Severity)
	}
	if f.RuleID != "GHSA-xxxx" || f.Module != "vulnerabilities" {
		t.Errorf("ruleID/module = %q/%q, want GHSA-xxxx/vulnerabilities", f.RuleID, f.Module)
	}
}

// TestToFindingNoVersionFallsBackToName: a package with no known version renders
// as a bare name (no trailing @).
func TestToFindingNoVersionFallsBackToName(t *testing.T) {
	f := toFinding(analysis.Vulnerability{
		ID:       "CVE-1",
		Packages: []string{"leftpad"},
		Verdict:  analysis.VerdictInfo,
	})
	want := "CVE-1: no description available (leftpad)"
	if f.Message != want {
		t.Errorf("message = %q, want %q", f.Message, want)
	}
}
