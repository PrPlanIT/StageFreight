package security

import "testing"

// When two scanners report the SAME (ID, Package) with DIFFERENT non-empty fix versions,
// the dedup must not silently keep one — it records the conflict + each source's fix so
// the ambiguity survives (rendered as "source-specific"), never a manufactured winner.
func TestDeduplicateVulnerabilities_PreservesFixConflict(t *testing.T) {
	in := []Vulnerability{
		{ID: "CVE-2026-13608", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", Source: "trivy"},
		{ID: "CVE-2026-13608", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.21.0-r1", Source: "grype"},
	}
	out := deduplicateVulnerabilities(in)
	if len(out) != 1 {
		t.Fatalf("want 1 merged entry, got %d", len(out))
	}
	v := out[0]
	if !v.FixedInConflict {
		t.Errorf("FixedInConflict must be set when scanners disagree on the fix")
	}
	if v.FixedInBySource["trivy"] != "8.22.0-r0" || v.FixedInBySource["grype"] != "8.21.0-r1" {
		t.Errorf("both sources' fixes must be preserved, got %v", v.FixedInBySource)
	}
}

// Scanners AGREEING on the same fix is not a conflict — clean single fix, no flag.
func TestDeduplicateVulnerabilities_AgreementIsNotConflict(t *testing.T) {
	in := []Vulnerability{
		{ID: "CVE-2026-13608", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", Source: "trivy"},
		{ID: "CVE-2026-13608", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", Source: "grype"},
	}
	out := deduplicateVulnerabilities(in)
	if len(out) != 1 || out[0].FixedInConflict || out[0].FixedIn != "8.22.0-r0" {
		t.Errorf("agreement must stay a clean single fix, got %+v", out[0])
	}
}
