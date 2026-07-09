package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
)

// TestFilterUpdateCandidates_PolicyMatrix locks the scoping principle:
//   - vulnerability remediation is a FLOOR — a vulnerable dep (direct OR indirect) is a
//     candidate under EVERY policy;
//   - freshness is the only policy axis — `all` updates non-vuln deps, `security` does not;
//   - non-vulnerable indirects float transitively (skipped) in both.
// The headline regression: a vulnerable INDIRECT under `all` must be remediated, not skipped
// as "indirect dependency" (the bug that left downstream repos with an unfixable critical).
func TestFilterUpdateCandidates_PolicyMatrix(t *testing.T) {
	vuln := []freshness.VulnInfo{{ID: "GHSA-5cv4-jp36-h3mw", Severity: "CRITICAL", FixedIn: "0.55.0"}}
	dep := func(indirect bool, vulns []freshness.VulnInfo, cur, latest string) freshness.Dependency {
		return freshness.Dependency{
			Name:            "golang.org/x/net",
			Ecosystem:       freshness.EcosystemGoMod, // auto-updatable
			File:            "go.mod",
			Current:         cur,
			Latest:          latest,
			Indirect:        indirect,
			Vulnerabilities: vulns,
		}
	}

	cases := []struct {
		name       string
		dep        freshness.Dependency
		policy     string
		wantCand   bool   // true = candidate; false = skipped
		wantReason string // expected skip reason when skipped
	}{
		{"vuln indirect / all → REMEDIATED (floor)", dep(true, vuln, "0.49.0", ""), "all", true, ""},
		{"vuln indirect / security → remediated", dep(true, vuln, "0.49.0", ""), "security", true, ""},
		{"non-vuln indirect / all → float", dep(true, nil, "1.0.0", "1.1.0"), "all", false, "indirect dependency"},
		{"non-vuln indirect / security → float", dep(true, nil, "1.0.0", "1.1.0"), "security", false, "indirect dependency"},
		{"non-vuln direct / all → freshness", dep(false, nil, "1.0.0", "1.1.0"), "all", true, ""},
		{"non-vuln direct / security → skip (no CVE)", dep(false, nil, "1.0.0", "1.1.0"), "security", false, "no CVE (security-only policy)"},
		{"vuln direct / security → remediated", dep(false, vuln, "1.0.0", "1.1.0"), "security", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cands, skipped := FilterUpdateCandidates([]freshness.Dependency{tc.dep}, UpdateConfig{Policy: tc.policy}, nil)
			if tc.wantCand {
				if len(cands) != 1 {
					t.Fatalf("want candidate, got skipped: %+v", skipped)
				}
				return
			}
			if len(skipped) != 1 {
				t.Fatalf("want skipped(%q), got candidate", tc.wantReason)
			}
			if skipped[0].Reason != tc.wantReason {
				t.Fatalf("skip reason = %q, want %q", skipped[0].Reason, tc.wantReason)
			}
		})
	}
}
