package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/lint/modules"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/spf13/cobra"
)

var (
	lintLevel    string
	lintModules  []string
	lintNoModule []string
	lintNoCache  bool
	lintAll      bool
)

var lintCmd = &cobra.Command{
	Use:   "lint [paths...]",
	Short: "Run code quality checks",
	Long: `Run cache-aware, delta-only code quality checks.

By default, only changed files are scanned (--level changed).
Use --level full or --all to scan everything.

Modules run in parallel and results are cached by content hash.`,
	RunE: runLint,
}

func init() {
	lintCmd.Flags().StringVar(&lintLevel, "level", "", "scan level: changed or full (default: from config, then changed)")
	lintCmd.Flags().StringSliceVar(&lintModules, "module", nil, "run only these modules (comma-separated)")
	lintCmd.Flags().StringSliceVar(&lintNoModule, "no-module", nil, "skip these modules (comma-separated)")
	lintCmd.Flags().BoolVar(&lintNoCache, "no-cache", false, "disable cache (clear and rescan)")
	lintCmd.Flags().BoolVar(&lintAll, "all", false, "scan all files (shorthand for --level full)")

	rootCmd.AddCommand(lintCmd)
}

func runLint(cmd *cobra.Command, args []string) error {
	if lintAll {
		lintLevel = "full"
	}
	// CLI flag > config > default "changed"
	if lintLevel == "" && cfg.Lint.Level != "" {
		lintLevel = string(cfg.Lint.Level)
	}
	if lintLevel == "" {
		lintLevel = "changed"
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	if len(args) > 0 {
		rootDir = args[0]
	}

	// Set up cache
	cacheDir := lint.ResolveCacheDir(rootDir, cfg.Lint.CacheDir)
	cache := &lint.Cache{
		Dir:     cacheDir,
		Enabled: !lintNoCache,
	}
	if lintNoCache {
		if err := cache.Clear(); err != nil && verbose {
			fmt.Fprintf(os.Stderr, "cache: clear failed: %v\n", err)
		}
	}

	engine, err := lint.NewEngine(cfg.Lint, rootDir, lintModules, lintNoModule, verbose, cache)
	if err != nil {
		return err
	}
	engine.ToolchainDesired = cfg.Toolchains.Desired

	if verbose {
		names := make([]string, len(engine.Modules))
		for i, m := range engine.Modules {
			names[i] = m.Name()
		}
		fmt.Fprintf(os.Stderr, "modules: %v\n", names)
	}

	// Collect all files
	files, err := engine.CollectFiles()
	if err != nil {
		return fmt.Errorf("collecting files: %w", err)
	}

	// Delta filtering — only scan changed files unless --level full
	if lintLevel != "full" {
		delta := &lint.Delta{RootDir: rootDir, TargetBranch: cfg.Lint.TargetBranch, Verbose: verbose}
		deltaCtx := context.Background()
		changedSet, deltaErr := delta.ChangedFiles(deltaCtx)
		if deltaErr != nil && verbose {
			fmt.Fprintf(os.Stderr, "delta: %v, falling back to full scan\n", deltaErr)
		}
		if changedSet != nil {
			allFiles := files
			files = lint.FilterByDelta(files, changedSet)
			if verbose {
				fmt.Fprintf(os.Stderr, "delta: %d/%d files changed\n", len(files), len(allFiles))
			}
		}
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "scanning %d files\n", len(files))
	}

	ctx := context.Background()
	ci := output.IsCI()
	color := output.UseColor()
	w := os.Stdout

	start := time.Now()
	findings, modStats, runErr := engine.RunWithStats(ctx, files)

	// Cross-file checks (filename collisions)
	findings = append(findings, modules.CheckFilenameCollisions(files)...)
	elapsed := time.Since(start)

	// Global sort for stable output
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		return a.Message < b.Message
	})

	// Tally
	var critical, warning, info int
	var totalFiles, totalCached int
	for _, f := range findings {
		switch f.Severity {
		case lint.SeverityCritical:
			critical++
		case lint.SeverityWarning:
			warning++
		case lint.SeverityInfo:
			info++
		}
	}
	for _, ms := range modStats {
		totalFiles += ms.Files
		totalCached += ms.Cached
	}

	// Write JUnit XML in CI for GitLab test reporting
	if ci {
		moduleNames := engine.ModuleNames()
		if jErr := output.WriteLintJUnit(".stagefreight/reports", findings, files, moduleNames, elapsed); jErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write junit report: %v\n", jErr)
		}
	}

	// ── Lint section ──
	output.SectionStart(w, "sf_lint", "Lint")
	sec := output.NewSection(w, "Lint", elapsed, color)
	output.LintTable(w, modStats, color)
	sec.Separator()
	sec.Row("%-16s%5d   %5d   %d findings (%d critical)",
		"total", totalFiles, totalCached, len(findings), critical)
	if u := engine.ClassifyUnreadable.Load(); u > 0 {
		sec.Row("%-16s%d unreadable (failed open → text)", "classify", u)
	}
	sec.Close()
	output.SectionEnd(w, "sf_lint")

	// ── Non-text disclosure (ungraded review surface — NEVER a finding) ──
	// The "HI, I'm a non-text blob — confirm I'm deliberate" surface: visible and
	// auditable without polluting the finding stream with false INFO/WARN/CRIT. Capped
	// inline for scale; the full list is always written to an artifact so "what non-text
	// appeared since last run" becomes diffable.
	renderNonTextDisclosure(w, engine.NonText, countModule(findings, "content"), color)

	// ── Findings section (only when findings > 0) ──
	if len(findings) > 0 {
		output.SectionStart(w, "sf_findings", "Findings")
		fSec := output.NewSection(w, "Findings", 0, color)
		output.SectionFindings(fSec, findings, color)
		fSec.Separator()
		fSec.Row("%s", output.FindingsSummaryLine(len(findings), critical, warning, info, len(files), color))
		fSec.Close()
		output.SectionEnd(w, "sf_findings")
	}

	// Cache stats
	if verbose && cache.Enabled {
		fmt.Fprintf(os.Stderr, "cache: %d hits, %d misses\n",
			engine.CacheHits.Load(), engine.CacheMisses.Load())
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", runErr)
	}

	if critical > 0 {
		return fmt.Errorf("lint failed: %d critical findings", critical)
	}

	return nil
}

// renderNonTextDisclosure prints the ungraded non-text inventory — the "validate these
// are deliberate" review surface — and always writes the full list to an artifact so it
// is diffable across runs. It is NOT a finding: no INFO/WARN/CRIT, so it can never
// become false-positive noise. Inline output is capped for scale.
func renderNonTextDisclosure(w io.Writer, nonText []lint.NonTextEntry, anomalies int, color bool) {
	if len(nonText) == 0 {
		return
	}
	writeNonTextArtifact(nonText)

	output.SectionStart(w, "sf_nontext", "non-text artifacts")
	sec := output.NewSection(w, "non-text artifacts (validate these are deliberate)", 0, color)
	const inlineCap = 10
	for i, e := range nonText {
		if i >= inlineCap {
			break
		}
		sec.Row("%-44s %s", e.Path, e.Type)
	}
	if len(nonText) > inlineCap {
		sec.Row("… +%d more → .stagefreight/reports/non-text.txt", len(nonText)-inlineCap)
	}
	sec.Separator()
	sec.Row("%d files · %d anomalies", len(nonText), anomalies)
	sec.Close()
	output.SectionEnd(w, "sf_nontext")
}

// writeNonTextArtifact persists the full non-text inventory for diffing across runs —
// the high-signal event is "what non-text APPEARED", not "non-text exists".
func writeNonTextArtifact(nonText []lint.NonTextEntry) {
	const dir = ".stagefreight/reports"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var b strings.Builder
	for _, e := range nonText {
		fmt.Fprintf(&b, "%s\t%s\n", e.Path, e.Type)
	}
	_ = os.WriteFile(filepath.Join(dir, "non-text.txt"), []byte(b.String()), 0o644)
}

func countModule(findings []lint.Finding, module string) int {
	n := 0
	for _, f := range findings {
		if f.Module == module {
			n++
		}
	}
	return n
}
