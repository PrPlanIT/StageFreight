// Package test is the StageFreight test subsystem: it resolves declared/synthesized
// test suites into native commands, runs them, and models the verdict. It owns
// suite resolution, command construction, execution, and aggregation; the lifecycle
// (audition adapter) consumes only the normalized verdict.
package test

import (
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Suite status values.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// SuiteResult is one suite's outcome — individually addressable for logs and
// future narrate/reporting. Coverage/Artifacts are reserved (not populated in v1).
type SuiteResult struct {
	ID       string
	Tool     config.TestTool
	Gate     config.Gate
	Status   string
	Duration time.Duration
	Output   string // captured combined stdout+stderr
	Err      error  // execution error (process failure / non-zero exit)
}

// TestResult aggregates per-suite outcomes.
type TestResult struct {
	Suites []SuiteResult
}

// FailedNonAdvisory reports whether any suite with a non-advisory gate failed.
// This is v1's entire gate verdict: the audition adapter fails the audition job
// (→ the cistate artifact is withheld → perform + downstream halt) when true.
func (r TestResult) FailedNonAdvisory() bool {
	for _, s := range r.Suites {
		if s.Status == StatusFailed && s.Gate != config.GateAdvisory {
			return true
		}
	}
	return false
}

// Failed reports whether any suite failed at all (advisory included), for reporting.
func (r TestResult) Failed() bool {
	for _, s := range r.Suites {
		if s.Status == StatusFailed {
			return true
		}
	}
	return false
}
