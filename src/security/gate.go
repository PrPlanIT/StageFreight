package security

import "strings"

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
