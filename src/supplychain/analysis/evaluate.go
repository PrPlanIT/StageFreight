package analysis

// evaluate assigns exactly one Verdict to a canonical vulnerability from its
// severity label. The mapping reproduces the severity→lint.Severity logic the
// freshness and osv modules used (CRITICAL/HIGH → critical, MODERATE → warning,
// everything else → info), so a canonical vulnerability's verdict matches the
// severity that source produced today. Pure.
func evaluate(v Vulnerability) Verdict {
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
