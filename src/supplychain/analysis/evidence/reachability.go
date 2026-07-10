package evidence

// ReachabilityEvidence is the FIRST evidence kind: whether a vulnerability's code is actually
// reachable in this program's call graph. It answers the question a scanner can't — not "is a
// vulnerable module present?" but "can the vulnerable code be called?". Produced by a
// reachability contributor (Go → govulncheck; Rust → experimental).
//
// Tri-state on purpose: "Unknown" (no analyzer for this ecosystem) is NOT "Unreachable".
// Collapsing them would silently hide vulnerabilities we simply could not analyze.
type ReachabilityEvidence struct {
	State      ReachabilityState
	Analyzer   string   // e.g. "govulncheck"
	Confidence Confidence
	Facts      []string // human-readable, e.g. "imported golang.org/x/crypto/ssh",
	//                      "no call path to golang.org/x/crypto/openpgp"
}

// Kind implements Evidence.
func (ReachabilityEvidence) Kind() string { return "reachability" }

// ReachabilityState is the reachability verdict.
type ReachabilityState int

const (
	ReachUnknown     ReachabilityState = iota // no analyzer ran for this ecosystem
	ReachReachable                            // an analyzer proved a call path
	ReachUnreachable                          // an analyzer proved the code never enters the call graph
)

func (s ReachabilityState) String() string {
	switch s {
	case ReachReachable:
		return "reachable"
	case ReachUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

// ParseReachabilityState is the inverse of String — it reconstructs a state from
// its serialized form (e.g. when reading a persisted catalogue). Unrecognized
// input is ReachUnknown (the fail-closed default).
func ParseReachabilityState(s string) ReachabilityState {
	switch s {
	case "reachable":
		return ReachReachable
	case "unreachable":
		return ReachUnreachable
	default:
		return ReachUnknown
	}
}

// Confidence qualifies an analyzer's verdict without gating policy — it is DISCLOSED to the
// operator (govulncheck is High; an experimental Rust analyzer may be Medium/Experimental) so
// they know how much to trust a downgrade.
type Confidence int

const (
	ConfidenceNone Confidence = iota
	ConfidenceExperimental
	ConfidenceMedium
	ConfidenceHigh
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceExperimental:
		return "experimental"
	default:
		return "none"
	}
}

// ParseConfidence is the inverse of String, for reconstructing from a persisted
// catalogue. Unrecognized input is ConfidenceNone.
func ParseConfidence(s string) Confidence {
	switch s {
	case "high":
		return ConfidenceHigh
	case "medium":
		return ConfidenceMedium
	case "experimental":
		return ConfidenceExperimental
	default:
		return ConfidenceNone
	}
}

// Reachability extracts the reachability evidence from a finding, if a contributor attached it.
func (f Finding) Reachability() (ReachabilityEvidence, bool) {
	for _, e := range f.Evidence {
		if r, ok := e.(ReachabilityEvidence); ok {
			return r, true
		}
	}
	return ReachabilityEvidence{}, false
}
