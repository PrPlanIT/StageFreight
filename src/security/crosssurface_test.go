package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
)

// writeSourceCatalogue writes a source-vulns.json under dir for the test.
func writeSourceCatalogue(t *testing.T, dir string, vulns []analysis.Vulnerability) {
	t.Helper()
	data, err := analysis.MarshalSourceAssessment(vulns)
	if err != nil {
		t.Fatal(err)
	}
	secDir := filepath.Join(dir, ".stagefreight", "security")
	if err := os.MkdirAll(secDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secDir, "source-vulns.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCrossSurface_CollapsesByID: an advisory seen in both source and image
// collapses to ONE vulnerability tagged [image source]; source-only and
// image-only advisories are classified correctly.
func TestCrossSurface_CollapsesByID(t *testing.T) {
	dir := t.TempDir()
	writeSourceCatalogue(t, dir, []analysis.Vulnerability{
		{ID: "CVE-2026-1", Severity: "HIGH", Packages: []string{"github.com/docker/docker@v28.5.2"}, Surfaces: []analysis.Surface{analysis.SurfaceSource}},
		{ID: "GO-2026-5932", Severity: "MODERATE", Packages: []string{"golang.org/x/crypto@v0.54.0"}, Surfaces: []analysis.Surface{analysis.SurfaceSource}},
	})

	imageVulns := []Vulnerability{
		{ID: "CVE-2026-1", Severity: "HIGH", Package: "github.com/docker/docker", Installed: "v28.5.2", Source: "trivy"}, // shared with source
		{ID: "CVE-2026-2", Severity: "CRITICAL", Package: "stdlib", Installed: "go1.25.8", Source: "grype"},             // image-only
	}

	cs := CrossSurface(dir, imageVulns)
	if cs == nil {
		t.Fatal("nil result")
	}
	if len(cs.Vulnerabilities) != 3 {
		t.Fatalf("want 3 collapsed advisories, got %d: %+v", len(cs.Vulnerabilities), cs.Vulnerabilities)
	}
	if cs.Both != 1 || cs.SourceOnly != 1 || cs.ImageOnly != 1 {
		t.Errorf("classification = both:%d source:%d image:%d, want 1/1/1", cs.Both, cs.SourceOnly, cs.ImageOnly)
	}
	for _, v := range cs.Vulnerabilities {
		if v.ID == "CVE-2026-1" && surfaceClass(v.Surfaces) != "both" {
			t.Errorf("shared advisory CVE-2026-1 surfaces = %v, want both", v.Surfaces)
		}
	}
}

// TestCrossSurface_ImageOnlyWhenNoCatalogue: absent source catalogue degrades to
// image-only (never fails).
func TestCrossSurface_ImageOnlyWhenNoCatalogue(t *testing.T) {
	cs := CrossSurface(t.TempDir(), []Vulnerability{{ID: "CVE-2026-9", Severity: "HIGH", Package: "x", Source: "trivy"}})
	if cs == nil || len(cs.Vulnerabilities) != 1 || cs.ImageOnly != 1 {
		t.Fatalf("want 1 image-only vuln, got %+v", cs)
	}
}

// TestCrossSurface_NilWhenNothing: no image vulns and no catalogue → nil.
func TestCrossSurface_NilWhenNothing(t *testing.T) {
	if CrossSurface(t.TempDir(), nil) != nil {
		t.Error("no image vulns + no catalogue should reconcile to nil")
	}
}
