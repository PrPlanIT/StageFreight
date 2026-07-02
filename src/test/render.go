package test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/substrate"
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

// RunRender realizes substrate, runs the suites, and renders ONE canonical section
// that everything streams INTO: an explained substrate note (what native tools were
// provisioned and WHY), each package's row as it finishes (no stranded output),
// failures expanded inline, then the coverage summary with the no-test packages
// collapsed to one callout. Derived from `go test -json`, never raw transport. The
// single presentation surface for the audition gate, deps re-verification, and the
// `stagefreight test` CLI. Returns the verdict.
func RunRender(ctx context.Context, suites []ResolvedSuite, rootDir string, desired map[string]config.ToolPinConfig, w io.Writer, intent Intent) *TestResult {
	color := output.UseColor()
	sec := output.NewSection(w, intent.title(), 0, color)

	if realized := realizeSubstrate(ctx, suites); len(realized) > 0 {
		renderSubstrate(sec, realized, color)
		sec.Separator()
	}

	res := &TestResult{}
	for i, s := range suites {
		if i > 0 {
			sec.Separator()
		}
		// Descriptive suite header — the verdict lives in the summary line below, so
		// this stays stable while packages stream in beneath it.
		head := fmt.Sprintf("%s   %s", s.ID, s.Tool)
		if cmd := cmdShape(s); cmd != "" {
			head += " · " + cmd
		}
		head += " · gate " + string(s.Gate)
		sec.Row("%s", head)
		if s.Synthesized && s.Provenance != "" {
			sec.Row("  [synthesized: %s]", s.Provenance)
		}

		sr := runSuite(ctx, rootDir, s, desired, func(p PackageResult) {
			renderPackageRow(sec, p, color)
		})
		res.Suites = append(res.Suites, sr)
		renderSuiteSummary(sec, sr, color)
	}

	sec.Close()
	return res
}

// renderPackageRow streams one package's result INTO the section as it finishes: a
// semantic row (module-relative path + doc synopsis), failures expanded inline. The
// no-test packages are omitted here — collapsed into the summary's one callout.
func renderPackageRow(sec *output.Section, p PackageResult, color bool) {
	if p.Status == StatusSkipped {
		return
	}
	sec.Row("  %s %-24s %6s %4s  %s", statusIcon(p.Status, color), p.Rel, durStr(p.Duration), covStr(p.Coverage), p.Synopsis)
	if p.Status == StatusFailed {
		for _, f := range p.Failures {
			sec.Row("      └ %s", f.Name)
			if line := firstErrLine(f.Output); line != "" {
				sec.Row("        %s", line)
			}
		}
	}
}

// renderSuiteSummary closes a suite with its verdict + coverage counts, and the
// no-test packages collapsed to ONE callout.
func renderSuiteSummary(sec *output.Section, sr SuiteResult, color bool) {
	if len(sr.Packages) == 0 {
		// Non-go suite (rust/script): no per-package projection.
		sec.Row("  %s %s", statusIcon(sr.Status, color), durStr(sr.Duration))
		if sr.Status == StatusFailed {
			if line := firstErrLine(sr.Output); line != "" {
				sec.Row("    %s", line)
			}
		}
		return
	}
	var tested, notest, failed, tests int
	for _, p := range sr.Packages {
		switch p.Status {
		case StatusSkipped:
			notest++
		case StatusFailed:
			tested, failed, tests = tested+1, failed+1, tests+p.Tests
		case StatusPassed:
			tested, tests = tested+1, tests+p.Tests
		}
	}
	line := fmt.Sprintf("%d tested · %d no-tests · %d failed · %d tests", tested, notest, failed, tests)
	if sr.Coverage >= 0 {
		line += fmt.Sprintf(" · %.1f%% cov", sr.Coverage)
		if sr.CoverageMin > 0 {
			mark := "✓"
			if sr.Coverage < sr.CoverageMin {
				mark = "✗"
			}
			line += fmt.Sprintf(" (min %.0f%% %s)", sr.CoverageMin, mark)
		}
	}
	line += " · " + durStr(sr.Duration)
	sec.Row("  %s %s", statusIcon(sr.Status, color), line)
	if notest > 0 {
		sec.Row("  ⓘ %d packages ship no tests", notest)
	}
}

// renderSubstrate explains the native capabilities realized for the run — WHAT was
// provisioned and WHY, in plain language (not raw substrate transport). This is the
// "let the user know what it means" surface for otherwise-cryptic toolchain loads.
func renderSubstrate(sec *output.Section, realized []substrate.Realized, color bool) {
	for _, r := range realized {
		var pkgs []string
		for _, p := range r.Packages {
			if p.Version != "" {
				pkgs = append(pkgs, p.Name+" "+p.Version)
			} else {
				pkgs = append(pkgs, p.Name)
			}
		}
		detail := strings.Join(pkgs, ", ")
		if detail == "" {
			detail = r.Need.Capability
		}
		if r.Present {
			detail += " (present)"
		}
		sec.Row("  ⚙ %-18s %s", detail, humanizeReason(r.Need.Reason))
	}
}

// humanizeReason turns a substrate Need.Reason token into a plain-language why.
func humanizeReason(reason string) string {
	switch reason {
	case "go-tests-exec-git":
		return "for git-based tests (fixtures, system-git transport)"
	case "go-test-race-cgo":
		return "C compiler for the race detector (cgo)"
	case "rust-build-script-linking":
		return "linker for the Rust build"
	case "crate-native-build", "":
		return "native build dependency"
	default:
		return reason
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

// covStr renders a coverage percentage, or "" when the package wasn't measured
// (coverage off) so the column collapses to blank space.
func covStr(c float64) string {
	if c < 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", c)
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
