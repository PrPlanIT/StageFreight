// Package severity is the dependency-free vocabulary for vulnerability severity:
// normalization, ranking, and ordering of the well-known labels (critical, high,
// medium, low, unknown). It owns severity SEMANTICS only — no CVEs, findings,
// scanners, or analysis types — so any layer (a domain, a gate, presentation)
// can depend DOWN on it without coupling to another's domain. Severity ordering
// is the ordering of a well-known vocabulary; no single domain owns it.
package severity

import "strings"

// The canonical severity vocabulary.
const (
	Critical = "CRITICAL"
	High     = "HIGH"
	Medium   = "MEDIUM"
	Low      = "LOW"
	Unknown  = "UNKNOWN"
)

// Normalize canonicalizes a severity label to the standard vocabulary. It folds
// the OSV "MODERATE" onto "MEDIUM" and maps empty or unrecognized input to
// "UNKNOWN".
func Normalize(label string) string {
	s := strings.ToUpper(strings.TrimSpace(label))
	if s == "MODERATE" {
		return Medium
	}
	switch s {
	case Critical, High, Medium, Low, Unknown:
		return s
	default:
		return Unknown
	}
}
