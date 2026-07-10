package lint

import "fmt"

// Summary is the single source of truth for rolling a set of findings up into the counts
// that drive BOTH presentation and the CI gate. The `stagefreight lint` CLI and the CI
// audition lint phase each summarize through this, so the gate policy — critical impact AND
// at least probable confidence — can never again diverge between the two paths (it did once:
// a heuristic-critical was non-blocking in the CLI but still failed the audition).
type Summary struct {
	Total    int
	Critical int // by severity
	Warning  int
	Info     int
	Blocking int // findings that fail CI: Severity==Critical && Confidence != Heuristic (Finding.Blocks)
}

// blockThreshold maps a fail_on label (lint's own importance vocabulary) to a
// (threshold, gateEnabled) pair. "off" disables the gate; empty/"critical" →
// Critical (today's default); "warning" → Warning; "info" → Info.
func blockThreshold(failOn string) (Severity, bool) {
	switch failOn {
	case "off":
		return SeverityCritical, false
	case "info":
		return SeverityInfo, true
	case "warning":
		return SeverityWarning, true
	default: // "critical" or empty
		return SeverityCritical, true
	}
}

// blocksAt reports whether a finding blocks at the given fail_on threshold — the
// module-outcomes Policy stage over a lint finding, in lint's own tier ordering.
func blocksAt(f Finding, threshold Severity, gateOn bool) bool {
	return gateOn && f.Severity >= threshold
}

// Summarize tallies findings by severity and counts the blocking subset at the
// given fail_on threshold (lint's importance vocabulary; "" → "critical", the
// historical default of blocking only critical findings).
func Summarize(findings []Finding, failOn string) Summary {
	s := Summary{Total: len(findings)}
	threshold, gateOn := blockThreshold(failOn)
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityWarning:
			s.Warning++
		case SeverityInfo:
			s.Info++
		}
		if blocksAt(f, threshold, gateOn) {
			s.Blocking++
		}
	}
	return s
}

// GateError is the one CI-gate verdict: non-nil iff the run should fail. Both lint paths
// return this, so the threshold and wording stay identical.
func (s Summary) GateError() error {
	if s.Blocking > 0 {
		return fmt.Errorf("lint failed: %d blocking finding(s)", s.Blocking)
	}
	return nil
}

// GateErrorSince is the baseline-aware CI verdict: it fails only on NEWLY-introduced blocking
// findings — those whose fingerprint is in isNew. Pre-existing findings (already present at
// the baseline) are surfaced but do not block, so a known, tracked, can't-fix-now advisory
// stays loud without wedging the gate, while a genuinely new regression still fails. label
// names the baseline in the failure message.
func GateErrorSince(findings []Finding, isNew map[string]bool, label, failOn string) error {
	threshold, gateOn := blockThreshold(failOn)
	n := 0
	for _, f := range findings {
		if blocksAt(f, threshold, gateOn) && isNew[f.Fingerprint()] {
			n++
		}
	}
	if n > 0 {
		return fmt.Errorf("lint failed: %d new blocking finding(s) since %s", n, label)
	}
	return nil
}

// CriticalNote renders the critical count, annotating the low-confidence non-blocking
// remainder when present: "1 critical, 1 low-confidence non-blocking".
func (s Summary) CriticalNote() string {
	if nb := s.Critical - s.Blocking; nb > 0 {
		return fmt.Sprintf("%d critical, %d low-confidence non-blocking", s.Critical, nb)
	}
	return fmt.Sprintf("%d critical", s.Critical)
}
