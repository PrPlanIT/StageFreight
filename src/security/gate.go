package security

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
	"github.com/PrPlanIT/StageFreight/src/vulnerability/severity"
)

// CountAtOrAbove returns how many of the scan's vulnerabilities are at or above
// the given severity threshold ("critical" | "high" | "medium" | "low"). "off"
// or any unrecognized threshold returns 0 — no gate.
func CountAtOrAbove(r *ScanResult, threshold string) int {
	switch strings.ToLower(strings.TrimSpace(threshold)) {
	case "low":
		return r.Critical + r.High + r.Medium + r.Low
	case "medium":
		return r.Critical + r.High + r.Medium
	case "high":
		return r.Critical + r.High
	case "critical":
		return r.Critical
	default: // "off" or unknown
		return 0
	}
}

// GatePolicy is everything the security gate decides on.
type GatePolicy struct {
	FailOn      string          // severity threshold; "off" gates nothing
	Unreachable string          // "pass" excuses vulnerabilities proven unreachable
	Unfixed     string          // "pass" excuses vulnerabilities with no published fix
	Excepted    map[string]bool // advisory IDs excused by an on-record exception
}

// excuses returns the predicate for a vulnerability the policy excuses: an exception
// (any policy), proven unreachable (Unreachable "pass"), or no fix published (Unfixed
// "pass").
func (p GatePolicy) excuses(cs *CrossSurfaceResult) func(Vulnerability) bool {
	ids := map[string]bool{}
	for id := range p.Excepted {
		ids[id] = true
	}
	if p.Unreachable == "pass" && cs != nil {
		for _, v := range cs.Vulnerabilities {
			if r, ok := reachabilityOf(v); !ok || r.State != evidence.ReachUnreachable {
				continue
			}
			ids[v.ID] = true
			for _, a := range v.Aliases {
				ids[a] = true
			}
		}
	}
	return func(v Vulnerability) bool {
		return ids[v.ID] || (p.Unfixed == "pass" && strings.TrimSpace(v.FixedIn) == "")
	}
}

// GatingCount returns how many vulnerabilities are at or above the threshold and not
// excused. It starts from the authoritative severity counts and subtracts the excused
// ones (from the complete, dedup'd list those counts derive from), so with nothing
// excused it equals CountAtOrAbove. FailOn "off" → 0.
func GatingCount(result *ScanResult, cs *CrossSurfaceResult, p GatePolicy) int {
	base := CountAtOrAbove(result, p.FailOn)
	if base == 0 {
		return base
	}
	minRank := severity.Rank(severity.Normalize(p.FailOn))
	excused, n := p.excuses(cs), 0
	for _, v := range result.Vulnerabilities {
		if severity.Rank(severity.Normalize(v.Severity)) >= minRank && excused(v) {
			n++
		}
	}
	if n > base {
		n = base
	}
	return base - n
}

// GatingVulns returns the vulnerabilities GatingCount counts, in scan-result order. Nil
// when the gate is off. This is the ROW source for audience text ({vulns}); the counts
// stay authoritative for the gate itself.
func GatingVulns(result *ScanResult, cs *CrossSurfaceResult, p GatePolicy) []Vulnerability {
	if p.FailOn == "" || strings.EqualFold(strings.TrimSpace(p.FailOn), "off") {
		return nil
	}
	minRank := severity.Rank(severity.Normalize(p.FailOn))
	excused := p.excuses(cs)
	var out []Vulnerability
	for _, v := range result.Vulnerabilities {
		if severity.Rank(severity.Normalize(v.Severity)) < minRank || excused(v) {
			continue
		}
		out = append(out, v)
	}
	return out
}
