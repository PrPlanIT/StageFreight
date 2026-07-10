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
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/spf13/cobra"
)

var (
	lintLevel    string
	lintModules  []string
	lintNoModule []string
	lintNoCache  bool
	lintAll      bool
	lintFixSafe  bool
	lintDryRun   bool
	lintBaseline bool
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
	lintCmd.Flags().BoolVar(&lintFixSafe, "fix-safe", false, "auto-apply proven-safe fixes (trailing whitespace, final newline) to authored files")
	lintCmd.Flags().BoolVar(&lintDryRun, "dry-run", false, "with --fix-safe: preview what would change without writing")
	lintCmd.Flags().BoolVar(&lintBaseline, "baseline", false, "diff against the merge-base: mark newly-introduced non-text artifacts and findings")

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

	// Use the caller's context when there is one — the audition path (validateRunner)
	// sets it so provision.Resolve records osv into the run ledger. Bare CLI invocation
	// has none; fall back so the lint command still works standalone.
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ci := output.IsCI()
	color := output.UseColor()
	w := os.Stdout

	// Discover dependencies ONCE and thread the Snapshot to the engine, mirroring the
	// audition path (depsRunner in ci_runners.go). Without this, the freshness and
	// vulnerabilities modules each self-resolve independently, running OSV correlation
	// and registry lookups twice over the same manifests. Options are sourced from the
	// same lint.modules.freshness.options section runDependencyUpdateLogic/
	// runPreBuildLintImpl use.
	//
	// Deliberately NOT the package-level discovery.Discover/Resolve helpers here: those
	// construct a bare Resolver with no ToolchainDesired, so a .stagefreight.yml pin
	// (toolchains.desired) would silently resolve zero dependencies — dropping every
	// freshness finding for pinned tool versions (cosign, flux, grype, …). The old
	// per-module self-resolution never had that gap because the engine calls
	// SetToolchainDesired on each module's own resolver. Build and configure a Resolver
	// the same way, then resolve each file once (ResolveFile is what self-resolution
	// called per module, so this is byte-for-byte the same resolution — just shared
	// instead of duplicated), scoped to exactly the files that will be scanned
	// (post-delta-filter).
	var freshnessOpts map[string]any
	if mc, ok := cfg.Lint.Modules["freshness"]; ok {
		freshnessOpts = mc.Options
	}
	resolver := discovery.NewResolver()
	resolver.SetToolchainDesired(cfg.Toolchains.Desired)
	if err := resolver.Configure(freshnessOpts); err != nil {
		return fmt.Errorf("configuring dependency resolver: %w", err)
	}
	var snapshotDeps []supplychain.Dependency
	for _, f := range files {
		deps, err := resolver.ResolveFile(ctx, f)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", f.Path, err)
		}
		snapshotDeps = append(snapshotDeps, deps...)
	}
	engine.Snapshot = &supplychain.Snapshot{Dependencies: snapshotDeps}

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

	// Tally (severity counts + blocking subset) via the shared summarizer, so this path and
	// the CI audition path (build/pipeline) gate identically.
	summary := lint.Summarize(findings, cfg.Lint.EffectiveFailOn())
	var totalFiles, totalCached int
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

	// Staged Tools box, in front of the Lint box (no-op with no run ledger, e.g. bare CLI).
	provision.StageBox(ctx, w, color)

	// ── Lint section ──
	output.SectionStart(w, "sf_lint", "Lint")
	sec := output.NewSection(w, "Lint", elapsed, color)
	output.LintTable(w, modStats, color)
	sec.Separator()
	sec.Row("%-16s%5d   %5d   %d findings (%s)",
		"total", totalFiles, totalCached, len(findings), summary.CriticalNote())
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
	// ── Baseline diff (opt-in via --baseline) ──
	// Resolve the merge-base once; Slice A (new non-text artifacts) uses its path set,
	// Slice B (new findings) uses its blob contents. Forgiving: no git / no base → no diff.
	var baseline *lint.Baseline
	var baseLabel string
	if lintBaseline {
		if b, ok, _ := lint.ResolveBaseline(rootDir, cfg.Lint.TargetBranch); ok {
			baseline, baseLabel = b, b.Commit
		} else if verbose {
			fmt.Fprintln(os.Stderr, "baseline: none resolved (no git repo / no base ref) — skipping diff")
		}
	}

	// Slice A — non-text artifacts present now but absent at the baseline are newly introduced.
	var newNonText map[string]bool
	if baseline != nil {
		if basePaths, err := baseline.Paths(); err == nil {
			newNonText = map[string]bool{}
			for _, e := range engine.NonText {
				if !basePaths[e.Path] {
					newNonText[e.Path] = true
				}
			}
		}
	}

	// Slice B — findings newly introduced relative to the baseline (by fingerprint).
	var newFindingFp map[string]bool
	if baseline != nil {
		if nf, derr := baseline.NewFindings(findings, cfg.Lint, rootDir, cache); derr == nil {
			newFindingFp = nf
		} else if verbose {
			fmt.Fprintf(os.Stderr, "baseline: finding diff failed: %v\n", derr)
		}
	}

	renderNonTextDisclosure(w, engine.NonText, countModule(findings, "content"), newNonText, baseLabel, color)

	// ── Provenance disclosure (ungraded; authored-hygiene relaxed, security intact) ──
	// Generated/vendored/lockfile files stay visible — we just didn't nag their
	// whitespace/length. Every secrets/CVE/concealment check still ran on them.
	renderProvenanceDisclosure(w, engine.NonAuthored, color)

	// ── Findings section (only when findings > 0) ──
	if len(findings) > 0 {
		output.SectionStart(w, "sf_findings", "Findings")
		fSec := output.NewSection(w, "Findings", 0, color)
		output.SectionFindings(fSec, findings, color)
		fSec.Separator()
		fSec.Row("%s", output.FindingsSummaryLine(len(findings), summary.Critical, summary.Warning, summary.Info, len(files), color))
		if baseLabel != "" {
			newCount := 0
			for _, f := range findings {
				if newFindingFp[f.Fingerprint()] {
					newCount++
				}
			}
			fSec.Row("%d new since %s (rest pre-existing or moved)", newCount, baseLabel)
		}
		fSec.Close()
		output.SectionEnd(w, "sf_findings")
	}

	// ── Safe remediation (opt-in via --fix-safe) ──
	// Applies only findings that carry a proven-safe Fix whose category is enabled.
	// Provenance-gated by construction: hygiene modules emit no findings (hence no Fix)
	// on generated/vendored/lockfile content, so only authored files are ever mutated.
	if lintFixSafe {
		rc := cfg.Lint.Remediation
		on := func(p *bool) bool { return p == nil || *p } // safe categories default ON
		enabled := map[string]bool{
			"trailing-whitespace": on(rc.TrailingWhitespace),
			"final-newline":       on(rc.FinalNewline),
		}
		sum, ferr := lint.ApplyRemediations(findings, rootDir, enabled, lintDryRun)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "fix-safe: %v\n", ferr)
		} else {
			renderRemediationSummary(w, sum, lintDryRun, color)
		}
	}

	// Cache stats
	if verbose && cache.Enabled {
		fmt.Fprintf(os.Stderr, "cache: %d hits, %d misses\n",
			engine.CacheHits.Load(), engine.CacheMisses.Load())
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", runErr)
	}

	// With a baseline active (--baseline), gate only on findings newly introduced relative to
	// the base ref — pre-existing ones are surfaced but do not fail CI. Otherwise gate on the
	// full blocking set.
	if baseline != nil && newFindingFp != nil {
		return lint.GateErrorSince(findings, newFindingFp, baseLabel, cfg.Lint.EffectiveFailOn())
	}
	return summary.GateError()
}

// renderRemediationSummary reports what --fix-safe changed (or, under --dry-run, would
// change). The findings shown above are pre-fix; a re-run confirms the reduced count.
func renderRemediationSummary(w io.Writer, sum lint.RemediationSummary, dryRun, color bool) {
	title := "fix-safe (authored files only — re-run lint to confirm)"
	verb := "edits across"
	if dryRun {
		title = "fix-safe --dry-run (preview — nothing written)"
		verb = "edits would change"
	}
	output.SectionStart(w, "sf_fixsafe", "fix-safe")
	sec := output.NewSection(w, title, 0, color)
	if sum.EditsApplied == 0 {
		sec.Row("no auto-fixable findings")
	} else {
		for _, kind := range []string{"trailing-whitespace", "final-newline"} {
			if n := sum.ByKind[kind]; n > 0 {
				sec.Row("  %-20s ×%d", kind, n)
			}
		}
		sec.Separator()
		sec.Row("%d %s %d files", sum.EditsApplied, verb, sum.FilesChanged)
	}
	if sum.Skipped > 0 {
		sec.Row("%d edits skipped (overlap)", sum.Skipped)
	}
	if sum.Drifted > 0 {
		sec.Row("%d files skipped (content changed since scan)", sum.Drifted)
	}
	sec.Close()
	output.SectionEnd(w, "sf_fixsafe")
}

// renderNonTextDisclosure prints the ungraded non-text inventory — the "validate these
// are deliberate" review surface — and always writes the full list to an artifact so it
// is diffable across runs. It is NOT a finding: no INFO/WARN/CRIT, so it can never
// become false-positive noise. Inline output is capped for scale.
func renderNonTextDisclosure(w io.Writer, nonText []lint.NonTextEntry, anomalies int, newPaths map[string]bool, baseLabel string, color bool) {
	if len(nonText) == 0 {
		return
	}
	writeNonTextArtifact(nonText)

	output.SectionStart(w, "sf_nontext", "non-text artifacts")
	sec := output.NewSection(w, "non-text artifacts (validate these are deliberate)", 0, color)
	const inlineCap = 10
	newCount := 0
	for i, e := range nonText {
		if newPaths[e.Path] { // nil map → always false; counts across the full set
			newCount++
		}
		if i >= inlineCap {
			continue
		}
		tag := ""
		if newPaths[e.Path] {
			tag = "  NEW"
		}
		sec.Row("%-44s %s%s", e.Path, e.Type, tag)
	}
	if len(nonText) > inlineCap {
		sec.Row("… +%d more → .stagefreight/reports/non-text.txt", len(nonText)-inlineCap)
	}
	sec.Separator()
	if baseLabel != "" {
		sec.Row("%d files · %d anomalies · %d new since %s", len(nonText), anomalies, newCount, baseLabel)
	} else {
		sec.Row("%d files · %d anomalies", len(nonText), anomalies)
	}
	sec.Close()
	output.SectionEnd(w, "sf_nontext")
}

// renderProvenanceDisclosure prints the ungraded provenance roll-up: the
// generated/vendored/lockfile files whose authored-code hygiene was relaxed. It is NOT a
// finding — provenance is descriptive, never severity-bearing. The point is visibility:
// these files were seen and routed, not silently dropped, and every security/supply-chain
// module still ran on them. Grouped by kind, sample paths capped for scale.
func renderProvenanceDisclosure(w io.Writer, entries []lint.ProvenanceEntry, color bool) {
	if len(entries) == 0 {
		return
	}
	byKind := map[string][]lint.ProvenanceEntry{}
	for _, e := range entries {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}

	output.SectionStart(w, "sf_provenance", "provenance")
	sec := output.NewSection(w, "provenance (authored-hygiene relaxed; secrets/CVE/concealment still ran)", 0, color)
	const inlineCap = 6
	for _, kind := range []string{"lockfile", "vendored", "generated"} {
		g := byKind[kind]
		if len(g) == 0 {
			continue
		}
		sample := make([]string, 0, inlineCap)
		for i, e := range g {
			if i >= inlineCap {
				break
			}
			sample = append(sample, e.Path)
		}
		suffix := strings.Join(sample, ", ")
		if len(g) > inlineCap {
			suffix = fmt.Sprintf("%s …(+%d)", suffix, len(g)-inlineCap)
		}
		sec.Row("  %-10s ×%-4d %s", kind, len(g), suffix)
	}
	sec.Separator()
	sec.Row("%d files · authored-hygiene relaxed (security checks unaffected)", len(entries))
	sec.Close()
	output.SectionEnd(w, "sf_provenance")
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
