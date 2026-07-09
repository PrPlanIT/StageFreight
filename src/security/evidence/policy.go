package evidence

import "strings"

// Verdict is the policy outcome for a finding.
type Verdict int

const (
	VerdictInfo Verdict = iota // informational — never blocks
	VerdictWarn                // surfaced, does not block
	VerdictFail                // blocks the gate
)

func (v Verdict) String() string {
	switch v {
	case VerdictFail:
		return "fail"
	case VerdictWarn:
		return "warn"
	default:
		return "info"
	}
}

// SeverityVerdict maps a raw scanner severity to the base verdict, BEFORE evidence.
func SeverityVerdict(severity string) Verdict {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high":
		return VerdictFail
	case "moderate", "medium":
		return VerdictWarn
	default: // low, negligible, unknown, empty
		return VerdictInfo
	}
}

// VerdictFor is the ENTIRE policy, data-driven over a finding's severity + evidence — no
// per-advisory or per-project special cases. Today only reachability evidence adjusts the base
// verdict; as KEV / EPSS / fix-availability land they slot in here as additional evidence reads,
// without changing the pipeline or the callers.
//
//	reachability: Unreachable → INFO (proven not called — surfaced, not blocking)
//	              Reachable / Unknown → base severity (fail-closed: no analyzer never downgrades)
//	(future)      KEV exploited → escalate; EPSS high → escalate; no fix → hold; …
//
// Confidence is disclosed via the evidence, not consulted here — a proven-unreachable is treated
// the same regardless of confidence (a min-confidence gate would be a config knob, not a rewrite).
func VerdictFor(f Finding) Verdict {
	base := SeverityVerdict(f.Vuln.Severity)

	if r, ok := f.Reachability(); ok && r.State == ReachUnreachable {
		return VerdictInfo
	}

	// Future evidence reads compose here, e.g.:
	//   if k, ok := f.KEV(); ok && k.Exploited { base = escalate(base) }
	//   if e, ok := f.EPSS(); ok && e.Percentile >= 0.97 { base = escalate(base) }

	return base
}
