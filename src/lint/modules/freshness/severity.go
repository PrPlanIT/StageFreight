package freshness

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// mapSeverity converts a VersionDelta into a lint.Severity using the
// configured severity levels and tolerance thresholds.
// Returns the severity and a human-readable summary, or ok=false if
// the delta is within tolerance on all axes.
func mapSeverity(delta VersionDelta, cfg SeverityConfig) (lint.Severity, string, bool) {
	// Determine the highest-priority axis that exceeds tolerance.
	// Major > Minor > Patch.
	type axis struct {
		label     string
		count     int
		tolerance int
		severity  int
	}
	axes := []axis{
		{"major", delta.Major, cfg.MajorTolerance, cfg.Major},
		{"minor", delta.Minor, cfg.MinorTolerance, cfg.Minor},
		{"patch", delta.Patch, cfg.PatchTolerance, cfg.Patch},
	}

	var best *axis
	for i := range axes {
		a := &axes[i]
		if a.count <= 0 {
			continue
		}
		if a.count <= a.tolerance {
			continue
		}
		if best == nil || a.severity > best.severity {
			best = a
		}
	}

	if best == nil {
		return 0, "", false
	}

	sev := intToSeverity(best.severity)
	msg := deltaMessage(delta)
	return sev, msg, true
}

// intToSeverity converts 0/1/2 from config to lint.Severity.
func intToSeverity(v int) lint.Severity {
	switch {
	case v >= 2:
		return lint.SeverityCritical
	case v == 1:
		return lint.SeverityWarning
	default:
		return lint.SeverityInfo
	}
}

// deltaMessage produces a summary like "1 major, 3 minor behind".
func deltaMessage(d VersionDelta) string {
	var parts []string
	if d.Major > 0 {
		parts = append(parts, fmt.Sprintf("%d major", d.Major))
	}
	if d.Minor > 0 {
		parts = append(parts, fmt.Sprintf("%d minor", d.Minor))
	}
	if d.Patch > 0 {
		parts = append(parts, fmt.Sprintf("%d patch", d.Patch))
	}
	if len(parts) == 0 {
		return "up to date"
	}
	msg := parts[0]
	for i := 1; i < len(parts); i++ {
		msg += ", " + parts[i]
	}
	return msg + " behind"
}
