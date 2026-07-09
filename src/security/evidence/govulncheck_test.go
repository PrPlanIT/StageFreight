package evidence

import (
	"context"
	"testing"
)

// The GO-2026-5932 finding below is the REAL govulncheck output captured from StageFreight:
// a module-only trace (golang.org/x/crypto, no package/function) — x/crypto is required but
// the openpgp package is never imported → unreachable. That is the case this whole layer exists
// to handle. The reachable + imported-not-called cases mirror govulncheck's other trace levels.
const govulncheckStream = `
{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck"}}
{"progress":{"message":"Scanning your code and 251 packages..."}}
{"osv":{"id":"GO-2026-5932","summary":"openpgp"}}
{"finding":{"osv":"GO-2026-5932","trace":[{"module":"golang.org/x/crypto","version":"v0.53.0"}]}}
{"finding":{"osv":"GO-2026-1111","trace":[{"module":"example.com/vuln","package":"example.com/vuln/bad","function":"Exploit"},{"module":"m","package":"m/app","function":"main"}]}}
{"finding":{"osv":"GO-2026-2222","trace":[{"module":"example.com/imp","package":"example.com/imp/pkg"}]}}
`

func TestParseGovulncheck_Levels(t *testing.T) {
	got := parseGovulncheck([]byte(govulncheckStream))

	// module-only trace → not imported → unreachable (the openpgp case).
	if got["GO-2026-5932"].State != ReachUnreachable {
		t.Fatalf("openpgp (module-only): want unreachable, got %s", got["GO-2026-5932"].State)
	}
	// a frame with a function → the symbol is called → reachable.
	if got["GO-2026-1111"].State != ReachReachable {
		t.Fatalf("called symbol: want reachable, got %s", got["GO-2026-1111"].State)
	}
	// package-only trace → imported but symbol not called → unreachable.
	if got["GO-2026-2222"].State != ReachUnreachable {
		t.Fatalf("imported-not-called: want unreachable, got %s", got["GO-2026-2222"].State)
	}
	// analyzer + confidence stamped, facts explain the verdict.
	e := got["GO-2026-5932"]
	if e.Analyzer != "govulncheck" || e.Confidence != ConfidenceHigh || len(e.Facts) == 0 {
		t.Fatalf("expected govulncheck/high with facts, got %+v", e)
	}
}

// Contribute maps findings onto the vulns being enriched, and leaves un-found vulns with NO
// evidence (Unknown, fail-closed) — the openpgp critical then downgrades to INFO end-to-end.
func TestGoReachability_Contribute(t *testing.T) {
	c := &GoReachability{Run: func(context.Context, string) ([]byte, error) {
		return []byte(govulncheckStream), nil
	}}
	if !c.Supports("go") || c.Supports("rust") {
		t.Fatal("Supports must be go-only")
	}

	openpgp := Vulnerability{ID: "GO-2026-5932", Ecosystem: "go", Package: "golang.org/x/crypto/openpgp", Severity: "CRITICAL"}
	unseen := Vulnerability{ID: "GO-2026-9999", Ecosystem: "go", Package: "whatever", Severity: "HIGH"}

	res, err := c.Contribute(context.Background(), Target{}, []Vulnerability{openpgp, unseen})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res[openpgp.Ref()].(ReachabilityEvidence)
	if !ok || r.State != ReachUnreachable {
		t.Fatalf("openpgp: want unreachable evidence, got %v (ok=%v)", res[openpgp.Ref()], ok)
	}
	if _, ok := res[unseen.Ref()]; ok {
		t.Fatal("a vuln govulncheck never reported must get NO evidence (stays Unknown)")
	}

	// End-to-end through the framework: enrich → policy downgrades the unreachable critical.
	findings := NewRegistry(c).Enrich(context.Background(), Target{}, []Vulnerability{openpgp})
	if VerdictFor(findings[0]) != VerdictInfo {
		t.Fatalf("unreachable critical must downgrade to INFO, got %s", VerdictFor(findings[0]))
	}
}
