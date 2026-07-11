package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// TestFilterUpdateCandidates_PolicyMatrix locks the scoping principle:
//   - vulnerability remediation is a FLOOR — a vulnerable dep (direct OR indirect) is a
//     candidate under EVERY policy;
//   - freshness is the only policy axis — `all` updates non-vuln deps, `security` does not;
//   - non-vulnerable indirects float transitively (skipped) in both.
// The headline regression: a vulnerable INDIRECT under `all` must be remediated, not skipped
// as "indirect dependency" (the bug that left downstream repos with an unfixable critical).
func TestFilterUpdateCandidates_PolicyMatrix(t *testing.T) {
	vuln := []supplychain.VulnInfo{{ID: "GHSA-5cv4-jp36-h3mw", Severity: "CRITICAL", FixedIn: "0.55.0"}}
	dep := func(indirect bool, vulns []supplychain.VulnInfo, cur, latest string) supplychain.Dependency {
		return supplychain.Dependency{
			Name:            "golang.org/x/net",
			Ecosystem:       supplychain.EcosystemGoMod, // auto-updatable
			File:            "go.mod",
			Current:         cur,
			Latest:          latest,
			Indirect:        indirect,
			Vulnerabilities: vulns,
		}
	}

	cases := []struct {
		name       string
		dep        supplychain.Dependency
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
			cands, skipped := FilterUpdateCandidates([]supplychain.Dependency{tc.dep}, UpdateConfig{Policy: tc.policy}, nil)
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

func TestUpdateTypeExceedsCeiling(t *testing.T) {
	cases := []struct {
		ut, ceiling string
		want        bool
	}{
		{"minor", "patch", true},     // minor exceeds a patch ceiling
		{"patch", "patch", false},    // patch within patch
		{"major", "minor", true},     // major exceeds minor
		{"minor", "minor", false},    // minor within minor
		{"major", "", false},         // empty ceiling defaults to MAJOR (no-op) — major does not exceed
		{"minor", "", false},         // minor within the major default
		{"minor", "major", false},    // nothing in-range exceeds major
		{"patch", "minor", false},    // patch within minor
		{"security", "patch", false}, // security treated as patch-level
	}
	for _, c := range cases {
		if got := updateTypeExceedsCeiling(c.ut, c.ceiling); got != c.want {
			t.Errorf("updateTypeExceedsCeiling(%q, %q) = %v, want %v", c.ut, c.ceiling, got, c.want)
		}
	}
}

// TestCeilingRetarget verifies that patch-lock re-targets to the newest in-range
// patch (via AvailableVersions) instead of holding on a minor bump — and still
// holds when no in-ceiling upgrade is available.
func TestCeilingRetarget(t *testing.T) {
	base := func() supplychain.Dependency {
		return supplychain.Dependency{
			Name:      "example.com/foo",
			Ecosystem: supplychain.EcosystemGoMod,
			File:      "go.mod",
			Current:   "1.2.3",
			Latest:    "1.3.0", // natural target is a MINOR bump
		}
	}

	t.Run("patch-lock re-targets to newest in-range patch", func(t *testing.T) {
		dep := base()
		dep.AvailableVersions = []string{"1.2.3", "1.2.5", "1.2.7", "1.3.0", "2.0.0"}
		cands, skipped := FilterUpdateCandidates([]supplychain.Dependency{dep}, UpdateConfig{MaxUpdate: "patch"}, nil)
		if len(cands) != 1 {
			t.Fatalf("want 1 candidate, got %d (skipped: %+v)", len(cands), skipped)
		}
		if cands[0].UpdateTarget() != "1.2.7" {
			t.Fatalf("UpdateTarget = %q, want re-target 1.2.7", cands[0].UpdateTarget())
		}
	})

	t.Run("patch-lock holds when no in-range patch exists", func(t *testing.T) {
		dep := base()
		dep.AvailableVersions = []string{"1.2.3", "1.3.0", "1.4.0"} // only minors above current
		cands, skipped := FilterUpdateCandidates([]supplychain.Dependency{dep}, UpdateConfig{MaxUpdate: "patch"}, nil)
		if len(cands) != 0 || len(skipped) != 1 {
			t.Fatalf("want held, got %d candidates / %d skipped", len(cands), len(skipped))
		}
		if skipped[0].Reason != "exceeds max_update ceiling (patch)" {
			t.Fatalf("reason = %q", skipped[0].Reason)
		}
	})

	t.Run("minor ceiling takes the natural minor (no re-target)", func(t *testing.T) {
		dep := base()
		dep.AvailableVersions = []string{"1.2.3", "1.2.7", "1.3.0"}
		cands, _ := FilterUpdateCandidates([]supplychain.Dependency{dep}, UpdateConfig{MaxUpdate: "minor"}, nil)
		if len(cands) != 1 || cands[0].UpdateTarget() != "1.3.0" {
			t.Fatalf("want natural 1.3.0, got %+v", cands)
		}
		if cands[0].ResolvedTarget != "" {
			t.Fatalf("ResolvedTarget should be empty under minor ceiling, got %q", cands[0].ResolvedTarget)
		}
	})
}

// TestSkipReasonCategories verifies the decision site emits the intrinsic typed
// category alongside the (unchanged) human reason.
func TestSkipReasonCategories(t *testing.T) {
	gomod := func(cur, latest string) supplychain.Dependency {
		return supplychain.Dependency{Name: "example.com/x", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod", Current: cur, Latest: latest}
	}
	cases := []struct {
		name    string
		dep     supplychain.Dependency
		cfg     UpdateConfig
		wantCat SkipCategory
		wantMsg string
	}{
		{"indirect", func() supplychain.Dependency { d := gomod("1.2.3", "1.3.0"); d.Indirect = true; return d }(), UpdateConfig{}, SkipIndirect, "indirect dependency"},
		{"up to date", gomod("1.2.3", "1.2.3"), UpdateConfig{}, SkipUpToDate, "up to date"},
		{"ceiling", gomod("1.2.3", "1.3.0"), UpdateConfig{MaxUpdate: "patch"}, SkipCeilingExceeded, "exceeds max_update ceiling (patch)"},
		{"security-only", gomod("1.2.3", "1.3.0"), UpdateConfig{Policy: "security"}, SkipSecurityOnly, "no CVE (security-only policy)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, msg := skipReason(tc.dep, tc.cfg, nil, nil)
			if cat != tc.wantCat {
				t.Errorf("category = %q, want %q", cat, tc.wantCat)
			}
			if msg != tc.wantMsg {
				t.Errorf("reason = %q, want %q (must stay verbatim)", msg, tc.wantMsg)
			}
		})
	}
}

// TestLockPending_RoutesAsCandidate: an unlocked wildcard toolchain (Current == Latest,
// nothing to bump) must still reach apply so its first lock is written — LockPending
// exempts it from the "up to date" skip, unlike an ordinary Current == Latest dep.
func TestLockPending_RoutesAsCandidate(t *testing.T) {
	base := supplychain.Dependency{
		Name: "trivy", Ecosystem: supplychain.EcosystemToolchain, File: ".stagefreight.yml",
		Current: "0.69.5", Latest: "0.69.5", // resolvable, but Current already equals target
	}
	// Without the flag it is up to date (nothing to do).
	if cat, _ := skipReason(base, UpdateConfig{}, nil, nil); cat != SkipUpToDate {
		t.Errorf("plain Current==Latest = %q, want up_to_date", cat)
	}
	// With LockPending it is a candidate (empty category = not skipped).
	pending := base
	pending.LockPending = true
	if cat, msg := skipReason(pending, UpdateConfig{}, nil, nil); cat != SkipNone {
		t.Errorf("LockPending must route as candidate, got %q/%q", cat, msg)
	}
}

// TestSkipReason_PinnedByReplace: a replace-governed dep is skipped (matching apply),
// not treated as unresolved despite an empty Latest.
func TestSkipReason_PinnedByReplace(t *testing.T) {
	dep := supplychain.Dependency{Name: "example.com/x", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod", Current: "1.0.0", Pinned: "replace directive"}
	cat, msg := skipReason(dep, UpdateConfig{}, nil, nil)
	if cat != SkipReplaceDirective || msg != "replace directive present" {
		t.Errorf("pinned dep skip = %q/%q, want replace_directive/'replace directive present'", cat, msg)
	}
}
