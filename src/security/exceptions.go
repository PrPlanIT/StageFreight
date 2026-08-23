package security

import (
	"strings"
	"time"
)

// Exception is a reviewed, on-record decision to excuse a specific advisory from the
// severity gate. It is the human counterpart to the reachability excusal in gate.go:
// where that excuses vulns an analyzer PROVED unreachable, this excuses vulns a person
// ACCEPTED (as not-applicable or an accepted risk). An excepted finding still appears in
// the scan report — the exception only stops it from FAILING the build.
type Exception struct {
	ID      string    // advisory ID this excepts (CVE / GHSA), matched case-insensitively
	Reason  string    // why it is accepted / not applicable — the record's justification
	Expires time.Time // zero = permanent; a date in the past lapses the exception (gates again)
	Package string    // optional: scope to one package name (empty = any package)
}

func (e Exception) matches(v Vulnerability) bool {
	if !strings.EqualFold(strings.TrimSpace(e.ID), strings.TrimSpace(v.ID)) {
		return false
	}
	if p := strings.TrimSpace(e.Package); p != "" && !strings.EqualFold(p, strings.TrimSpace(v.Package)) {
		return false
	}
	return true
}

func (e Exception) expired(now time.Time) bool {
	return !e.Expires.IsZero() && e.Expires.Before(now)
}

// ExceptedFinding pairs an excused vulnerability with the reason it was excused.
type ExceptedFinding struct {
	Vuln   Vulnerability
	Reason string
}

// ExceptionResolution is the outcome of applying a set of exceptions to a scan result as
// of a point in time. ExceptedIDs feeds the gate; the remaining fields drive the report so
// no excusal is silent and stale ones surface loudly.
type ExceptionResolution struct {
	ExceptedIDs map[string]bool   // advisory IDs actively excepted — passed to the gate
	Excepted    []ExceptedFinding // excused findings + their reasons — shown in the report
	Expired     []Exception       // lapsed exceptions that still match a finding — surfaced; they gate
	Permanent   []Exception       // active, no-expiry exceptions that matched — standing decisions
	Unused      []Exception       // exceptions matching no current finding — candidates for removal
}

// ResolveExceptions applies exceptions to a scan result as of now. An active (non-expired)
// exception matching a finding excuses that finding from the gate and is recorded in
// Excepted. An expired exception does NOT excuse — the finding gates again and the lapse is
// recorded in Expired. Exceptions matching no finding land in Unused; active no-expiry ones
// that matched land in Permanent. Reachability excusal is unaffected — the two compose.
func ResolveExceptions(result *ScanResult, exceptions []Exception, now time.Time) ExceptionResolution {
	res := ExceptionResolution{ExceptedIDs: map[string]bool{}}
	if result == nil || len(exceptions) == 0 {
		return res
	}
	for _, e := range exceptions {
		matchedAny, matchedActive := false, false
		for _, v := range result.Vulnerabilities {
			if !e.matches(v) {
				continue
			}
			matchedAny = true
			if e.expired(now) {
				continue // lapsed: does not excuse; the finding gates
			}
			matchedActive = true
			res.ExceptedIDs[v.ID] = true
			res.Excepted = append(res.Excepted, ExceptedFinding{Vuln: v, Reason: e.Reason})
		}
		switch {
		case !matchedAny:
			res.Unused = append(res.Unused, e)
		case e.expired(now):
			res.Expired = append(res.Expired, e)
		case e.Expires.IsZero() && matchedActive:
			res.Permanent = append(res.Permanent, e)
		}
	}
	return res
}
