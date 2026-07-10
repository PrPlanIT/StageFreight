package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// TestCrossSurface_CarriesReachability: reachability attached to a source record
// survives the persist→read→collapse round-trip onto the collapsed vulnerability,
// and the disclosure line shows it.
func TestCrossSurface_CarriesReachability(t *testing.T) {
	dir := t.TempDir()
	writeSourceCatalogue(t, dir, []analysis.Vulnerability{{
		ID: "CVE-2026-1", Severity: "HIGH",
		Packages: []string{"github.com/docker/docker@v28.5.2"},
		Surfaces: []analysis.Surface{analysis.SurfaceSource},
		Evidence: []evidence.Evidence{evidence.ReachabilityEvidence{
			State: evidence.ReachUnreachable, Analyzer: "govulncheck",
			Confidence: evidence.ConfidenceHigh, Facts: []string{"not imported"},
		}},
	}})
	cs := CrossSurface(dir, []Vulnerability{
		{ID: "CVE-2026-1", Severity: "HIGH", Package: "github.com/docker/docker", Installed: "v28.5.2", Source: "trivy"},
	})
	if cs == nil || cs.Both != 1 {
		t.Fatalf("want 1 both-surface advisory, got %+v", cs)
	}
	rr, ok := reachabilityOf(cs.Vulnerabilities[0])
	if !ok || rr.State != evidence.ReachUnreachable {
		t.Errorf("reachability not carried onto collapsed vuln: ok=%v state=%v", ok, rr.State)
	}
	lines := cs.DisclosureLines()
	if len(lines) != 1 || !strings.Contains(lines[0], "CVE-2026-1") || !strings.Contains(lines[0], "unreachable") {
		t.Errorf("disclosure = %v, want CVE-2026-1 [source+image] [unreachable]", lines)
	}
}

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
	if cs.SourceFound {
		t.Error("SourceFound should be false when no catalogue is present (drives the CI degradation warning)")
	}
}

// TestCrossSurface_NilWhenNothing: no image vulns and no catalogue → nil.
func TestCrossSurface_NilWhenNothing(t *testing.T) {
	if CrossSurface(t.TempDir(), nil) != nil {
		t.Error("no image vulns + no catalogue should reconcile to nil")
	}
}

// TestCrossSurface_NoReachabilityCrossContamination: two DISTINCT advisories that
// only share a non-primary alias (CVE-A) must not swap reachability. GHSA-Y is
// reachable; GHSA-Z (same alias, no reachability) must NOT inherit it.
func TestCrossSurface_NoReachabilityCrossContamination(t *testing.T) {
	dir := t.TempDir()
	writeSourceCatalogue(t, dir, []analysis.Vulnerability{
		{
			ID: "GHSA-Y", Aliases: []string{"CVE-A"}, Severity: "HIGH",
			Surfaces: []analysis.Surface{analysis.SurfaceSource},
			Evidence: []evidence.Evidence{evidence.ReachabilityEvidence{
				State: evidence.ReachReachable, Analyzer: "govulncheck", Confidence: evidence.ConfidenceHigh,
			}},
		},
		{ID: "GHSA-Z", Aliases: []string{"CVE-A"}, Severity: "HIGH", Surfaces: []analysis.Surface{analysis.SurfaceSource}},
	})

	cs := CrossSurface(dir, nil)
	if cs == nil {
		t.Fatal("nil result")
	}
	var y, z *analysis.Vulnerability
	for i := range cs.Vulnerabilities {
		switch cs.Vulnerabilities[i].ID {
		case "GHSA-Y":
			y = &cs.Vulnerabilities[i]
		case "GHSA-Z":
			z = &cs.Vulnerabilities[i]
		}
	}
	if y == nil || z == nil {
		t.Fatalf("want GHSA-Y and GHSA-Z as distinct advisories, got %+v", cs.Vulnerabilities)
	}
	if _, ok := reachabilityOf(*y); !ok {
		t.Error("GHSA-Y should keep its own reachability")
	}
	if _, ok := reachabilityOf(*z); ok {
		t.Error("GHSA-Z must NOT inherit GHSA-Y's reachability via the shared alias CVE-A")
	}
}

// TestSplitPkgVersion covers scoped-npm names (leading @) and normal cases.
func TestSplitPkgVersion(t *testing.T) {
	cases := []struct{ in, name, ver string }{
		{"golang.org/x/crypto@v0.54.0", "golang.org/x/crypto", "v0.54.0"},
		{"@scope/pkg", "@scope/pkg", ""},
		{"@scope/pkg@1.2.3", "@scope/pkg", "1.2.3"},
		{"lodash", "lodash", ""},
	}
	for _, c := range cases {
		if n, v := splitPkgVersion(c.in); n != c.name || v != c.ver {
			t.Errorf("splitPkgVersion(%q) = (%q, %q), want (%q, %q)", c.in, n, v, c.name, c.ver)
		}
	}
}
