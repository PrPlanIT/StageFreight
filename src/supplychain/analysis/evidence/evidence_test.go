package evidence

import (
	"context"
	"errors"
	"testing"
)

// fakeReach is a stand-in Go reachability contributor (the real one shells govulncheck).
type fakeReach struct {
	verdict map[string]ReachabilityEvidence // affected package → evidence
	err     error
}

func (fakeReach) Name() string             { return "fake-govulncheck" }
func (fakeReach) Supports(eco string) bool { return eco == "go" }
func (f fakeReach) Contribute(_ context.Context, _ Target, vulns []Vulnerability) (map[VulnRef]Evidence, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[VulnRef]Evidence{}
	for _, v := range vulns {
		if e, ok := f.verdict[v.Package]; ok {
			out[v.Ref()] = e
		}
	}
	return out, nil
}

// End-to-end: enrich discovered vulns, then read the attached reachability evidence. Covers the
// three load-bearing cases — unreachable evidence attached with facts, reachable attached, and
// no-analyzer leaves the finding without evidence (Unknown, fail-closed). The verdict those
// states drive is tested at the analysis level.
func TestEnrich_And_Policy(t *testing.T) {
	openpgp := Vulnerability{ID: "GO-2026-5932", Ecosystem: "go", Package: "golang.org/x/crypto/openpgp", Severity: "CRITICAL", Source: "osv"}
	reachableGo := Vulnerability{ID: "GO-2026-1111", Ecosystem: "go", Package: "github.com/foo/bar", Severity: "HIGH", Source: "osv"}
	npmCrit := Vulnerability{ID: "CVE-2026-9", Ecosystem: "npm", Package: "lodash", Severity: "CRITICAL", Source: "osv"}

	reg := NewRegistry(fakeReach{verdict: map[string]ReachabilityEvidence{
		"golang.org/x/crypto/openpgp": {State: ReachUnreachable, Analyzer: "govulncheck", Confidence: ConfidenceHigh,
			Facts: []string{"imported golang.org/x/crypto/ssh", "no call path to golang.org/x/crypto/openpgp"}},
		"github.com/foo/bar": {State: ReachReachable, Analyzer: "govulncheck", Confidence: ConfidenceHigh},
	}})
	findings := reg.Enrich(context.Background(), Target{}, []Vulnerability{openpgp, reachableGo, npmCrit})

	byID := map[string]Finding{}
	for _, f := range findings {
		byID[f.Vuln.ID] = f
	}

	// openpgp: unreachable evidence attached, with facts that explain why (no ignore list).
	if r, ok := byID["GO-2026-5932"].Reachability(); !ok || r.State != ReachUnreachable || len(r.Facts) == 0 {
		t.Fatalf("openpgp: expected unreachable evidence with facts, got %+v (ok=%v)", r, ok)
	}
	// reachable Go high: reachable evidence attached.
	if r, ok := byID["GO-2026-1111"].Reachability(); !ok || r.State != ReachReachable {
		t.Fatalf("reachable go high: expected reachable evidence, got %+v (ok=%v)", r, ok)
	}
	// npm critical: no Go analyzer covers it → no evidence → Unknown (fail-closed).
	if _, ok := byID["CVE-2026-9"].Reachability(); ok {
		t.Fatal("npm must have no reachability evidence (no analyzer)")
	}
}

// A contributor that errors must add no evidence, leaving the finding at its scanner severity.
func TestEnrich_ContributorErrorIsFailClosed(t *testing.T) {
	v := Vulnerability{ID: "GO-x", Ecosystem: "go", Package: "p", Severity: "CRITICAL"}
	reg := NewRegistry(fakeReach{err: errors.New("govulncheck failed")})
	findings := reg.Enrich(context.Background(), Target{}, []Vulnerability{v})
	if _, ok := findings[0].Reachability(); ok {
		t.Fatal("errored contributor must attach no evidence")
	}
}
