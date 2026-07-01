package test

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// Intent distinguishes WHY the suites ran, so the canonical render surface reads
// correctly for each caller. The dependency re-verification run is NOT "the test
// stage again" — it validates a proposed graph mutation — so it gets its own header.
type Intent int

const (
	IntentCorrectness Intent = iota // audition gate: "is the committed tree healthy?"
	IntentDepReverify               // deps: "did the mutation preserve health?"
)

func (i Intent) title() string {
	if i == IntentDepReverify {
		return "Verify Upgrade"
	}
	return "Test"
}

const passedShown = 6 // slowest-N passed packages listed before "… N more"

// Render writes the canonical, house-style section: per-package semantic rows
// (module-relative path + doc synopsis), a coverage summary, the no-test packages
// collapsed to ONE callout, and expanded failures. Derived from `go test -json`,
// never raw transport. Shared by the audition gate, deps re-verification, and the
// `stagefreight test` CLI — one presentation surface for every caller.
func Render(w io.Writer, suites []ResolvedSuite, res *TestResult, intent Intent) {
	color := output.UseColor()
	var total time.Duration
	for _, s := range res.Suites {
		total += s.Duration
	}
	sec := output.NewSection(w, intent.title(), total, color)
	prov := map[string]string{}
	for _, s := range suites {
		if s.Synthesized {
			prov[s.ID] = s.Provenance
		}
	}
	for i, s := range res.Suites {
		if i > 0 {
			sec.Separator()
		}
		renderSuite(sec, s, prov[s.ID], color)
	}
	sec.Close()
}

func renderSuite(sec *output.Section, s SuiteResult, provenance string, color bool) {
	head := fmt.Sprintf("%s  %s", statusIcon(s.Status, color), s.ID)
	if provenance != "" {
		head += "   [synthesized: " + provenance + "]"
	}
	sec.Row("%s", head)

	if len(s.Packages) == 0 {
		// Non-go suite (rust/script): no per-package detail to project.
		sec.Row("   %s", durStr(s.Duration))
		return
	}

	var tested, notest, tests int
	var passed, failed []PackageResult
	for _, p := range s.Packages {
		switch p.Status {
		case StatusSkipped:
			notest++
		case StatusFailed:
			tested, tests = tested+1, tests+p.Tests
			failed = append(failed, p)
		case StatusPassed:
			tested, tests = tested+1, tests+p.Tests
			passed = append(passed, p)
		}
	}

	sec.Row("   %d tested · %d no-tests · %d failed · %d tests · %s",
		tested, notest, len(failed), tests, durStr(s.Duration))

	// Failures first — most important, fully expanded with the failing test + error.
	for _, p := range failed {
		sec.Row("   %s %-24s %6s  %s", statusIcon(StatusFailed, color), p.Rel, durStr(p.Duration), p.Synopsis)
		for _, f := range p.Failures {
			sec.Row("       └ %s", f.Name)
			if line := firstErrLine(f.Output); line != "" {
				sec.Row("         %s", line)
			}
		}
	}

	// Passed packages: the slowest few (where time/risk lives), then a "… N more".
	sort.SliceStable(passed, func(i, j int) bool { return passed[i].Duration > passed[j].Duration })
	for i, p := range passed {
		if i >= passedShown {
			break
		}
		sec.Row("   %s %-24s %6s  %s", statusIcon(StatusPassed, color), p.Rel, durStr(p.Duration), p.Synopsis)
	}
	if n := len(passed) - passedShown; n > 0 {
		sec.Row("   … %d more passed", n)
	}

	// The [no test files] spam, collapsed to a single operationally-useful callout.
	if notest > 0 {
		sec.Row("   ⓘ %d packages ship no tests", notest)
	}
}

// statusIcon maps the test subsystem's status to the output package's icon vocab.
func statusIcon(status string, color bool) string {
	switch status {
	case StatusPassed:
		return output.StatusIcon("success", color)
	case StatusFailed:
		return output.StatusIcon("failed", color)
	default:
		return output.StatusIcon("skipped", color)
	}
}

func durStr(d time.Duration) string {
	if d >= time.Second || d == 0 {
		return d.Truncate(100 * time.Millisecond).String()
	}
	return d.Truncate(time.Millisecond).String()
}
