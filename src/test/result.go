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
	ID          string
	Tool        config.TestTool
	Gate        config.Gate
	Status      string
	Duration    time.Duration
	Packages    []PackageResult // per-package detail (Go suites, parsed from -json)
	Coverage    float64         // statement-weighted suite total %; <0 = not measured
	CoverageMin float64         // gate threshold %; 0 = none
	Output      string          // captured stderr (build errors etc.)
	Err         error           // execution error (process failure / non-zero exit)
}

// PackageResult is one package's outcome, parsed from `go test -json` — the
// StageFreight-native presentation derived from go's transport stream. `Rel` is the
// module-relative path (preserves WHERE it ran without the long import prefix);
// `Synopsis` is the package doc-comment one-liner (what it IS, for non-test-writers).
type PackageResult struct {
	ImportPath string // github.com/PrPlanIT/StageFreight/src/commit
	Rel        string // module-relative: src/commit
	Synopsis   string // package doc synopsis (go list .Doc); may be empty
	Status     string // StatusPassed | StatusFailed | StatusSkipped (no test files)
	Duration   time.Duration
	Tests      int           // top-level tests run
	Coverage   float64       // statement coverage %; <0 means "not measured"
	Failures   []TestFailure // leaf failures (with output), when Status==failed
}

// TestFailure is a single failing (leaf) test and its captured output.
type TestFailure struct {
	Name   string
	Output string
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
