package test

import (
	"fmt"
	"io"
	"sort"
	"strings"
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
	byID := make(map[string]ResolvedSuite, len(suites))
	for _, s := range suites {
		byID[s.ID] = s
	}
	sec := output.NewSection(w, intent.title(), total, color)
	for i, s := range res.Suites {
		if i > 0 {
			sec.Separator()
		}
		renderSuite(sec, s, byID[s.ID], color)
	}
	sec.Close()
}

func renderSuite(sec *output.Section, s SuiteResult, rs ResolvedSuite, color bool) {
	// Suite header: icon id   tool · <command shape> · gate <gate>.
	head := fmt.Sprintf("%s %s   %s", statusIcon(s.Status, color), s.ID, s.Tool)
	if cmd := cmdShape(rs); cmd != "" {
		head += " · " + cmd
	}
	head += " · gate " + string(s.Gate)
	sec.Row("%s", head)
	if rs.Synthesized && rs.Provenance != "" {
		sec.Row("   [synthesized: %s]", rs.Provenance)
	}

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

// cmdShape is the human-readable command a suite runs, minus the binary and
// subcommand — go [test] "-race ./...", rust [test] "--workspace", script the raw
// command. It's what makes the suite line say what actually runs.
func cmdShape(rs ResolvedSuite) string {
	if len(rs.Argv) <= 2 {
		return ""
	}
	return strings.Join(rs.Argv[2:], " ")
}
