package analysis

import "github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"

// evaluate assigns exactly one Verdict to a canonical vulnerability. It consults
// attached evidence BEFORE severity: a PROVEN-unreachable vulnerability is
// surfaced as Info rather than blocking. This is fail-closed — only a proven
// Unreachable state downgrades; Reachable, Unknown, or absent evidence never
// changes the severity-derived verdict. Absent any evidence the mapping reduces
// to severityVerdict (CRITICAL/HIGH → critical, MODERATE → warning, else → info),
// so a canonical vulnerability's verdict matches the severity that source
// produced today. Pure.
func evaluate(v Vulnerability) Verdict {
	for _, e := range v.Evidence {
		if r, ok := e.(evidence.ReachabilityEvidence); ok && r.State == evidence.ReachUnreachable {
			return VerdictInfo // proven-unreachable: surfaced, not blocking (fail-closed: only Unreachable downgrades)
		}
	}
	return severityVerdict(v.Severity)
}

// severityVerdict maps an OSV severity label to a Verdict.
func severityVerdict(label string) Verdict {
	switch normalizeLabel(label) {
	case "CRITICAL", "HIGH":
		return VerdictCritical
	case "MODERATE":
		return VerdictWarning
	default:
		return VerdictInfo
	}
}
