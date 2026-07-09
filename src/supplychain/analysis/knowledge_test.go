package analysis

import (
	"reflect"
	"testing"
)

// TestCanonicalizeMergesByAliasIntersection: an OSV-API observation and an
// osv-scanner observation that name the same advisory under different IDs (they
// share an alias) collapse into ONE canonical vulnerability — taking the highest
// severity, unioning packages and aliases.
func TestCanonicalizeMergesByAliasIntersection(t *testing.T) {
	obs := []AdvisoryObservation{
		{
			Source: "osv-api", VulnID: "GHSA-xxxx", Package: "golang.org/x/net",
			Ecosystem: "gomod", Severity: "LOW", Summary: "api summary",
			File: "go.mod", Line: 7,
		},
		{
			Source: "osv-scanner", VulnID: "GO-2026-5932", Aliases: []string{"GHSA-xxxx", "CVE-2026-1"},
			Package: "golang.org/x/net", Ecosystem: "gomod", Severity: "MODERATE",
			FixedIn: "0.40.0", Summary: "scanner summary", File: "go.mod",
		},
	}

	vulns := canonicalize(obs)
	if len(vulns) != 1 {
		t.Fatalf("want 1 canonical vulnerability, got %d: %+v", len(vulns), vulns)
	}
	v := vulns[0]
	// Canonical ID is the lexicographically smallest primary id: "GHSA-xxxx" < "GO-2026-5932".
	if v.ID != "GHSA-xxxx" {
		t.Errorf("canonical ID = %q, want smallest primary GHSA-xxxx", v.ID)
	}
	if v.Severity != "MODERATE" {
		t.Errorf("severity = %q, want highest (MODERATE)", v.Severity)
	}
	if v.FixedIn != "0.40.0" {
		t.Errorf("fixedIn = %q, want 0.40.0", v.FixedIn)
	}
	if v.File != "go.mod" {
		t.Errorf("file = %q, want go.mod (OSV-API representative)", v.File)
	}
	wantAliases := []string{"CVE-2026-1", "GO-2026-5932"}
	if !reflect.DeepEqual(v.Aliases, wantAliases) {
		t.Errorf("aliases = %v, want %v", v.Aliases, wantAliases)
	}
	if !reflect.DeepEqual(v.Packages, []string{"golang.org/x/net"}) {
		t.Errorf("packages = %v, want [golang.org/x/net]", v.Packages)
	}
}

// TestCanonicalizeSharedNonPrimaryAliasStaysSeparate (fix #4): two DISTINCT
// advisories that merely cross-reference a common CVE alias must NOT collapse.
// A={GHSA-A, CVE-X} and B={GHSA-B, CVE-X} share CVE-X, but neither's PRIMARY id
// is contained in the other's id-set, so they remain two vulnerabilities (a bare
// id-set intersection would wrongly merge them and lose one advisory).
func TestCanonicalizeSharedNonPrimaryAliasStaysSeparate(t *testing.T) {
	obs := []AdvisoryObservation{
		{
			Source: "osv-api", VulnID: "GHSA-A", Aliases: []string{"CVE-X"},
			Package: "pkg-a", Severity: "HIGH", File: "go.mod",
		},
		{
			Source: "osv-scanner", VulnID: "GHSA-B", Aliases: []string{"CVE-X"},
			Package: "pkg-b", Severity: "MODERATE", File: "go.mod",
		},
	}
	vulns := canonicalize(obs)
	if len(vulns) != 2 {
		t.Fatalf("want 2 distinct vulnerabilities (shared non-primary alias must NOT merge), got %d: %+v", len(vulns), vulns)
	}
	if vulns[0].ID != "GHSA-A" || vulns[1].ID != "GHSA-B" {
		t.Errorf("ids = [%s %s], want [GHSA-A GHSA-B]", vulns[0].ID, vulns[1].ID)
	}
}

// TestCanonicalizePrimaryContainmentMerges (fix #4): an OSV-API observation whose
// PRIMARY id (GO-2026-5932) appears in an osv-scanner observation's id-set (as an
// alias) is the SAME advisory → ONE vulnerability.
func TestCanonicalizePrimaryContainmentMerges(t *testing.T) {
	obs := []AdvisoryObservation{
		{
			Source: "osv-api", VulnID: "GO-2026-5932", Package: "golang.org/x/net",
			Version: "0.38.0", Severity: "HIGH", File: "go.mod", Line: 5,
		},
		{
			Source: "osv-scanner", VulnID: "GHSA-zzzz", Aliases: []string{"GO-2026-5932", "CVE-2026-9"},
			Package: "golang.org/x/net", Version: "0.38.0", Severity: "CRITICAL",
			FixedIn: "0.40.0", File: "go.mod",
		},
	}
	vulns := canonicalize(obs)
	if len(vulns) != 1 {
		t.Fatalf("want 1 canonical vulnerability (primary id carried as scanner alias), got %d: %+v", len(vulns), vulns)
	}
	v := vulns[0]
	// Canonical ID is the lexicographically smallest primary: "GHSA-zzzz" < "GO-2026-5932".
	if v.ID != "GHSA-zzzz" {
		t.Errorf("canonical ID = %q, want smallest primary GHSA-zzzz", v.ID)
	}
	if v.Severity != "CRITICAL" {
		t.Errorf("severity = %q, want highest (CRITICAL)", v.Severity)
	}
	// fix #6: the affected package carries its version for triage.
	if !reflect.DeepEqual(v.Packages, []string{"golang.org/x/net@0.38.0"}) {
		t.Errorf("packages = %v, want [golang.org/x/net@0.38.0]", v.Packages)
	}
}

// TestCanonicalizeKeepsDistinctAdvisoriesSeparate: two advisories with no shared
// identifier stay separate, and output is deterministically ordered by ID.
func TestCanonicalizeKeepsDistinctAdvisoriesSeparate(t *testing.T) {
	obs := []AdvisoryObservation{
		{Source: "osv-api", VulnID: "GHSA-bbbb", Package: "b", Severity: "HIGH", File: "go.mod"},
		{Source: "osv-api", VulnID: "GHSA-aaaa", Package: "a", Severity: "CRITICAL", File: "go.mod"},
	}
	vulns := canonicalize(obs)
	if len(vulns) != 2 {
		t.Fatalf("want 2 distinct vulnerabilities, got %d", len(vulns))
	}
	if vulns[0].ID != "GHSA-aaaa" || vulns[1].ID != "GHSA-bbbb" {
		t.Errorf("order = [%s %s], want sorted [GHSA-aaaa GHSA-bbbb]", vulns[0].ID, vulns[1].ID)
	}
}

// TestEvaluateMirrorsSeverityMapping: verdict assignment reproduces the
// freshness/osv severity→lint mapping.
func TestEvaluateMirrorsSeverityMapping(t *testing.T) {
	cases := map[string]Verdict{
		"CRITICAL": VerdictCritical,
		"HIGH":     VerdictCritical,
		"MODERATE": VerdictWarning,
		"LOW":      VerdictInfo,
		"UNKNOWN":  VerdictInfo,
		"":         VerdictInfo,
	}
	for label, want := range cases {
		if got := evaluate(Vulnerability{Severity: label}); got != want {
			t.Errorf("evaluate(%q) = %v, want %v", label, got, want)
		}
	}
}
