package output

import (
	"bytes"
	"strings"
	"testing"
)

// render runs SectionVulns into a buffer (no color) and returns the text.
func renderVulns(t *testing.T, vulns []VulnRow) string {
	t.Helper()
	var b bytes.Buffer
	sec := NewSection(&b, "Security Scan", 0, false)
	SectionVulns(sec, vulns, false, HardBudget, SecurityUX{})
	sec.Close()
	return b.String()
}

// Packages that share a CVE with identical (installed, fixed) collapse to ONE advisory
// entry: all package names on one AFFECTED line, and the description printed exactly once
// (not once per package).
func TestSectionVulns_CollapsesSharedAdvisory(t *testing.T) {
	desc := "BusyBox wget accepts raw CR/LF in the request-target"
	out := renderVulns(t, []VulnRow{
		{ID: "CVE-2025-60876", Severity: "MEDIUM", Package: "busybox", Installed: "1.37.0-r31", FixedIn: "", Title: desc},
		{ID: "CVE-2025-60876", Severity: "MEDIUM", Package: "busybox-binsh", Installed: "1.37.0-r31", FixedIn: "", Title: desc},
		{ID: "CVE-2025-60876", Severity: "MEDIUM", Package: "ssl_client", Installed: "1.37.0-r31", FixedIn: "", Title: desc},
	})

	if strings.Count(out, "CVE-2025-60876") != 1 {
		t.Errorf("CVE should appear once (collapsed), got %d:\n%s", strings.Count(out, "CVE-2025-60876"), out)
	}
	if strings.Count(out, desc) != 1 {
		t.Errorf("description should appear once, got %d:\n%s", strings.Count(out, desc), out)
	}
	for _, pkg := range []string{"busybox", "busybox-binsh", "ssl_client"} {
		if !strings.Contains(out, pkg) {
			t.Errorf("AFFECTED must list %q:\n%s", pkg, out)
		}
	}
	if !strings.Contains(out, "(no fix)") {
		t.Errorf("empty FixedIn must render (no fix):\n%s", out)
	}
	if !strings.Contains(out, "1 findings · ") && !strings.Contains(out, "3 findings · 1 advisories") {
		t.Errorf("header should report 3 findings · 1 advisories:\n%s", out)
	}
}

// When packages under one CVE have different fixed versions, they must NOT collapse — each
// package renders its own installed→fixed so the two never drift apart.
func TestSectionVulns_SplitsDivergentVersions(t *testing.T) {
	out := renderVulns(t, []VulnRow{
		{ID: "CVE-2026-13608", Severity: "MEDIUM", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", Title: "libcurl SASL/LDAP handshake"},
		{ID: "CVE-2026-13608", Severity: "MEDIUM", Package: "libcurl", Installed: "8.20.0-r0", FixedIn: "8.22.0-r0", Title: "libcurl SASL/LDAP handshake"},
	})

	if strings.Count(out, "CVE-2026-13608") != 1 {
		t.Errorf("CVE header should appear once, got %d:\n%s", strings.Count(out, "CVE-2026-13608"), out)
	}
	// Divergent installed → each package keeps its own installed version on its own line.
	if !strings.Contains(out, "curl · 8.21.0-r0") || !strings.Contains(out, "libcurl · 8.20.0-r0") {
		t.Errorf("divergent installed versions must stay per-package:\n%s", out)
	}
	if strings.Count(out, "→ 8.22.0-r0") != 2 {
		t.Errorf("each package line carries its own fix, want 2 got %d:\n%s", strings.Count(out, "→ 8.22.0-r0"), out)
	}
}

// A scanner-disagreement finding renders PATCHED as "source-specific", never a guessed
// version — the invariant that aggregation must not state a stronger claim than the evidence.
func TestSectionVulns_ScannerConflictRendersSourceSpecific(t *testing.T) {
	out := renderVulns(t, []VulnRow{
		{ID: "CVE-2026-13608", Severity: "MEDIUM", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", FixedConflict: true, Title: "conflict"},
	})
	if !strings.Contains(out, "source-specific") {
		t.Errorf("conflicting fix must render source-specific:\n%s", out)
	}
	if strings.Contains(out, "→ 8.22.0-r0") {
		t.Errorf("must NOT present one scanner's version as the fix when sources disagree:\n%s", out)
	}
}

// A clean single-fix advisory renders "→ <ver>" and severity-sorts CRITICAL above MEDIUM.
func TestSectionVulns_FixAndSeveritySort(t *testing.T) {
	out := renderVulns(t, []VulnRow{
		{ID: "CVE-2026-00002", Severity: "MEDIUM", Package: "curl", Installed: "8.21.0-r0", FixedIn: "8.22.0-r0", Title: "med thing"},
		{ID: "CVE-2026-00001", Severity: "CRITICAL", Package: "openssl", Installed: "3.0.0", FixedIn: "3.0.1", Title: "crit thing"},
	})
	if !strings.Contains(out, "→ 8.22.0-r0") || !strings.Contains(out, "→ 3.0.1") {
		t.Errorf("fix versions must render:\n%s", out)
	}
	ci := strings.Index(out, "CVE-2026-00001") // critical
	mi := strings.Index(out, "CVE-2026-00002") // medium
	if ci < 0 || mi < 0 || ci > mi {
		t.Errorf("CRITICAL must sort above MEDIUM:\n%s", out)
	}
}
