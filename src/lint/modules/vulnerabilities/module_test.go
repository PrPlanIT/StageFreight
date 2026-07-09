package vulnerabilities

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
)

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
