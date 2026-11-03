package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
// evidence (Unknown, fail-closed).
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
}

// A vulnerability discovered under a CVE/GHSA id still correlates to govulncheck's GO-advisory
// finding via its aliases — OSV ID is never the correlation contract.
func TestGoReachability_CorrelatesByAlias(t *testing.T) {
	c := &GoReachability{Run: func(context.Context, string) ([]byte, error) {
		return []byte(govulncheckStream), nil
	}}
	// Discovery reported this under a CVE, with the GO advisory only as an alias.
	discovered := Vulnerability{ID: "CVE-2026-55555", Aliases: []string{"GHSA-aaaa-bbbb-cccc", "GO-2026-5932"},
		Ecosystem: "go", Package: "golang.org/x/crypto/openpgp", Severity: "CRITICAL"}

	res, err := c.Contribute(context.Background(), Target{}, []Vulnerability{discovered})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res[discovered.Ref()].(ReachabilityEvidence)
	if !ok || r.State != ReachUnreachable {
		t.Fatalf("expected join via GO-alias → unreachable evidence, got %v (ok=%v)", res[discovered.Ref()], ok)
	}
}

// fakeGovulncheck puts a `govulncheck` on PATH running script.
func fakeGovulncheck(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "govulncheck"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A run that dies partway is an error naming how it died, never a parsed stream.
func TestRunGovulncheck_FailedRunsAreErrors(t *testing.T) {
	t.Run("killed mid-stream", func(t *testing.T) {
		fakeGovulncheck(t, `echo '{"finding":{"osv":"GO-2024-0001","trace":[{"module":"m"}]}}'; kill -9 $$`)
		if _, err := runGovulncheck(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "killed") {
			t.Fatalf("err = %v, want a killed run reported", err)
		}
	})
	t.Run("exited non-zero", func(t *testing.T) {
		fakeGovulncheck(t, `echo "loading packages: go.mod not found" >&2; exit 1`)
		if _, err := runGovulncheck(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "go.mod not found") {
			t.Fatalf("err = %v, want the exit and its stderr", err)
		}
	})
	t.Run("complete run", func(t *testing.T) {
		fakeGovulncheck(t, `echo '{"finding":{"osv":"GO-2024-0001","trace":[{"module":"m"}]}}'`)
		out, err := runGovulncheck(context.Background(), "")
		if err != nil || len(out) == 0 {
			t.Fatalf("out=%q err=%v, want the stream", out, err)
		}
	})
}
