package security

import (
	"testing"
	"time"
)

func TestResolveExceptions(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	result := &ScanResult{
		Critical: 1, High: 1,
		Vulnerabilities: []Vulnerability{
			{ID: "CVE-1", Severity: "CRITICAL", Package: "openssl"},
			{ID: "CVE-2", Severity: "HIGH", Package: "busybox"},
		},
	}
	exceptions := []Exception{
		{ID: "CVE-1", Reason: "not applicable", Expires: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}, // active
		{ID: "CVE-2", Reason: "accepted risk", Expires: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},    // lapsed
		{ID: "CVE-9", Reason: "already patched upstream"},                                               // matches nothing
	}
	res := ResolveExceptions(result, exceptions, now)

	if !res.ExceptedIDs["CVE-1"] {
		t.Errorf("CVE-1 (active) should be excepted")
	}
	if res.ExceptedIDs["CVE-2"] {
		t.Errorf("CVE-2 is expired; it must gate, not be excepted")
	}
	if len(res.Excepted) != 1 || res.Excepted[0].Vuln.ID != "CVE-1" || res.Excepted[0].Reason != "not applicable" {
		t.Errorf("Excepted = %+v, want one CVE-1 with its reason", res.Excepted)
	}
	if len(res.Expired) != 1 || res.Expired[0].ID != "CVE-2" {
		t.Errorf("Expired = %+v, want [CVE-2]", res.Expired)
	}
	if len(res.Unused) != 1 || res.Unused[0].ID != "CVE-9" {
		t.Errorf("Unused = %+v, want [CVE-9]", res.Unused)
	}
}

func TestResolveExceptions_PackageScopeAndPermanent(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	result := &ScanResult{
		High:            1,
		Vulnerabilities: []Vulnerability{{ID: "CVE-1", Severity: "HIGH", Package: "openssl"}},
	}

	// Wrong package → no match → unused, never excepted.
	res := ResolveExceptions(result, []Exception{{ID: "CVE-1", Reason: "x", Package: "apache2"}}, now)
	if res.ExceptedIDs["CVE-1"] {
		t.Errorf("package mismatch must not except")
	}
	if len(res.Unused) != 1 {
		t.Errorf("wrong-package exception should be unused; got %+v", res.Unused)
	}

	// Right package, no expiry → excepted AND flagged permanent (standing decision).
	res = ResolveExceptions(result, []Exception{{ID: "CVE-1", Reason: "x", Package: "openssl"}}, now)
	if !res.ExceptedIDs["CVE-1"] {
		t.Errorf("matching package must except")
	}
	if len(res.Permanent) != 1 {
		t.Errorf("matched no-expiry exception should be permanent; got %+v", res.Permanent)
	}
}

func TestGatingCountExcusesException(t *testing.T) {
	result := &ScanResult{
		Critical: 2,
		Vulnerabilities: []Vulnerability{
			{ID: "CVE-1", Severity: "CRITICAL"},
			{ID: "CVE-2", Severity: "CRITICAL"},
		},
	}
	// No exceptions → both gate (even with policy "fail", nil cs).
	if got := GatingCount(result, nil, nil, "critical", "fail"); got != 2 {
		t.Errorf("no exception: got %d, want 2", got)
	}
	// Except CVE-1 → one remains, and it works under policy "fail" (exceptions are
	// independent of reachability).
	excepted := map[string]bool{"CVE-1": true}
	if got := GatingCount(result, nil, excepted, "critical", "fail"); got != 1 {
		t.Errorf("with exception: got %d, want 1", got)
	}
	if v := GatingVulns(result, nil, excepted, "critical", "fail"); len(v) != 1 || v[0].ID != "CVE-2" {
		t.Errorf("GatingVulns with exception = %+v, want [CVE-2]", v)
	}
}
