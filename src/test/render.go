package test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
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

	// Provisioning phase: realize native capabilities and resolve each suite's
	// toolchain, then render the environment ledger in ITS OWN box, separate from the
	// results. Toolchains are resolved here (cache-warm) so their provenance + trust
	// land in the ledger; runSuite then executes against the captured Result.
	ledger := provision.FromSubstrate(realizeSubstrate(ctx, suites))
	tools := make([]toolchain.Result, len(suites))
	toolErrs := make([]error, len(suites))
	seen := map[string]bool{}
	for i, s := range suites {
		res, err := resolveSuiteToolchain(rootDir, s)
		tools[i], toolErrs[i] = res, err
		if err == nil && res.Tool != "" {
			if k := res.Tool + "@" + res.Version; !seen[k] {
				seen[k] = true
				ledger = append(ledger, provision.FromToolchain(res, ""))
			}
		}
	}
	provision.Render(w, ledger, color)

	// Results phase: one box, only about tests.
	sec := output.NewSection(w, intent.title(), 0, color)
	res := &TestResult{}
	for i, s := range suites {
		if i > 0 {
			sec.Separator()
		}
		sec.Row("%s", suiteHeader(s))
		if s.Synthesized && s.Provenance != "" {
			sec.Row("  [synthesized: %s]", s.Provenance)
		}

		var sr SuiteResult
		switch {
		case toolErrs[i] != nil:
			sr = failSuite(SuiteResult{ID: s.ID, Tool: s.Tool, Gate: s.Gate},
				fmt.Errorf("resolving %s toolchain: %w", s.Tool, toolErrs[i]))
		case producesPackageRows(s.Tool):
			// Per-package detail can be long (dozens of packages), so fold it in a CI
			// section — VISIBLE by default, collapsible. Seeing every test is the trust
			// feature; we never omit, only allow folding. Suite header above and summary
			// below stay unfolded. No-op outside GitLab CI.
			// GitLab prepends a ▸ toggle and appends a duration badge to this line and
			// renders it OUTSIDE our box frame, so we can't frame it — but we style the
			// label as a box-divider so it reads as an intentional collapsible sub-header
			// of the Test box rather than a stray phrase.
			output.SectionStart(w, "sf_test_"+s.ID, "──── per-package results ────")
			sec.Row("    %-24s %6s %4s  %s", "package", "time", "cov", "description")
			sr = runSuite(ctx, rootDir, s, tools[i], desired, func(p PackageResult) {
				renderPackageRow(sec, p, color)
			})
			output.SectionEnd(w, "sf_test_"+s.ID)
		default:
			sr = runSuite(ctx, rootDir, s, tools[i], desired, nil)
		}
		res.Suites = append(res.Suites, sr)
		renderSuiteSummary(sec, sr, color)
	}

	sec.Close()
	return res
}

// suiteHeader renders a suite as operator-readable intent, not implementation tokens:
//
//	▸ unit — go test -race -cover ./... · gate: perform
func suiteHeader(s ResolvedSuite) string {
	verb := string(s.Tool) + " test"
	if s.Tool == config.TestToolScript {
		verb = "script"
	}
	head := "▸ " + s.ID + " — " + verb
	if run := cmdShape(s); run != "" {
		head += " " + run
	}
	return head + " · gate: " + string(s.Gate)
}

// producesPackageRows reports whether a suite streams per-package/per-binary rows
// (go and rust do; a script produces a single captured result), so we only print the
// package/time/cov/description header where those columns actually apply.
func producesPackageRows(tool config.TestTool) bool {
	return tool == config.TestToolGo || tool == config.TestToolRust
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

// covStr renders a coverage percentage, or "" when there's nothing meaningful to show
// — either not measured (c<0) or a package whose own tests cover ~none of it (0%).
// Blanking the 0% keeps the column signal-only instead of a wall of alarming zeros;
// the exact 0-vs-unmeasured distinction lives in verbose/JSON.
func covStr(c float64) string {
	if c <= 0 {
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
