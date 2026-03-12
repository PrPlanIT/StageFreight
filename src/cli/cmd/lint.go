package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/lint/modules"
	"github.com/PrPlanIT/StageFreight/src/output"
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
	sec.Close()
	output.SectionEnd(w, "sf_lint")

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
