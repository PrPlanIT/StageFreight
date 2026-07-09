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
