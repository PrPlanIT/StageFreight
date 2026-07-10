package security

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// SeverityRank maps a severity label to a comparable rank (higher = worse).
// Unknown/empty is 0. "moderate" is treated as "medium" (OSV vs CVSS vocab).
func SeverityRank(label string) int {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium", "moderate":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

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

// GatingCount returns how many vulnerabilities are at or above the fail-on
// threshold and NOT excused. A vulnerability is excused only when
// unreachablePolicy is "pass" AND the cross-surface reconciliation proved it
// unreachable. It starts from the authoritative severity counts and subtracts
// the excused delta (computed from the complete, dedup'd vuln list that those
// counts are derived from), so with policy "fail" or a nil cs it equals
// CountAtOrAbove exactly — no behavior change on that path. failOn "off" → 0.
func GatingCount(result *ScanResult, cs *CrossSurfaceResult, failOn, unreachablePolicy string) int {
	base := CountAtOrAbove(result, failOn)
	if base == 0 || unreachablePolicy != "pass" || cs == nil {
		return base
	}
	minRank := SeverityRank(failOn)

	// Advisory ids (and aliases) the cross-surface reconciliation proved unreachable.
	excusedIDs := map[string]bool{}
	for _, v := range cs.Vulnerabilities {
		if r, ok := reachabilityOf(v); !ok || r.State != evidence.ReachUnreachable {
			continue
		}
		excusedIDs[v.ID] = true
		for _, a := range v.Aliases {
			excusedIDs[a] = true
		}
	}
	if len(excusedIDs) == 0 {
		return base
	}

	excused := 0
	for _, v := range result.Vulnerabilities {
		if excusedIDs[v.ID] && SeverityRank(v.Severity) >= minRank {
			excused++
		}
	}
	if excused > base {
		excused = base
	}
	return base - excused
}
