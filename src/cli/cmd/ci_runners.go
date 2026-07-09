package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/auditionproof"
	"github.com/PrPlanIT/StageFreight/src/build"
	_ "github.com/PrPlanIT/StageFreight/src/build/contributors" // register build-strategy contributors
	_ "github.com/PrPlanIT/StageFreight/src/build/docker"       // register the crucible contributor
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/dependency"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitops"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/runner"
	stagefreightsync "github.com/PrPlanIT/StageFreight/src/sync"
	"github.com/PrPlanIT/StageFreight/src/test"
	"github.com/PrPlanIT/StageFreight/src/trace"
	"github.com/spf13/cobra"
)

// buildCIRegistry returns a registry of all CI subsystem runners.
// All runner implementations live here in cmd — ci package stays pure types.
func buildCIRegistry() ci.Registry {
	return ci.Registry{
		// Canonical lifecycle phase commands — used by all CI skeletons.
		"audition": auditionPhaseRunner,
		"perform":  performPhaseRunner,
		"review":   reviewPhaseRunner,
		"publish":  publishPhaseRunner,
		"narrate":  narratePhaseRunner,
		// Legacy compatibility aliases — kept for local dev and migration.
		"build":     buildRunner,
		"deps":      depsRunner,
		"docs":      docsRunner,
		"reconcile": reconcileRunner,
		"release":   releaseRunner,
		"security":  securityRunner,
		"validate":  validateRunner,
	}
}

// resolveWorkspace returns the workspace directory from CI context or cwd.
func resolveWorkspace(ciCtx *ci.CIContext) string {
	if ciCtx.Workspace != "" {
		return ciCtx.Workspace
	}
	dir, _ := os.Getwd()
	return dir
}

// ── build runner ─────────────────────────────────────────────────────────────
// Runs binary builds first (if any), then docker builds.
// Binary builds execute before docker builds to satisfy depends_on ordering.
func buildRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	// Policy gate: skip non-release tags (e.g., rolling "latest" tag)
	if ciCtx.IsTag() && !tagMatchesReleasePolicy(ciCtx.Tag, appCfg.Versioning) {
		fmt.Printf("  build: skipping — tag %q does not match any release tag source\n", ciCtx.Tag)
		return nil
	}

	rootDir := resolveWorkspace(ciCtx)

	// Resolve build policy: if ANY build is required, the subsystem is required.
	buildRequired := false
	for _, b := range appCfg.Builds {
		if b.IsRequired() {
			buildRequired = true
		}
	}
	hasBuilds := len(appCfg.Builds) > 0
	if !hasBuilds {
		buildRequired = true // no builds configured = subsystem still required (reports not_applicable)
	}

	// Initialize pipeline state with CI context
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.CI = cistate.InitFromCI(ciCtx)
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "build", Attempted: true, Required: buildRequired, Outcome: "failed",
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}

	// No builds configured → not_applicable.
	if !hasBuilds {
		fmt.Fprintln(os.Stderr, "build: no builds configured — skipping")
		if ciCtx.IsCI() {
			if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
				st.Build.ProducedImages = false
				st.RecordSubsystem(cistate.SubsystemState{
					Name: "build", Attempted: true, Required: buildRequired,
					Outcome: "not_applicable", Reason: "no builds configured",
				})
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
			}
		}
		return nil
	}

	// ONE domain-ordered run: the binary and docker/crucible contributors feed
	// the shared domains (Detect/Plan/Build/Verify/Publish); the run renders one
	// Identity/Executor/Summary and writes the single outputs.json/published.json
	// pair. Replaces the former two-pipeline dispatch (and its manifest clobber).
	var runOutputs artifact.OutputsManifest
	runRB := build.NewResultsBuilder()
	store := cas.NewWorkspaceStore(rootDir)
	rc := &domains.RunContext{
		Ctx:     ctx,
		RootDir: rootDir,
		Config:  appCfg,
		Writer:  os.Stdout,
		Stderr:  os.Stderr,
		Color:   output.UseColor(),
		Verbose: opts.Verbose,
		Store:   store,
		Outputs: &runOutputs,
		RB:      runRB,
		// Perform prints a slim one-line provenance stamp; the full logo banner
		// belongs to audition. (Standalone build commands leave HeaderFull.)
		Header: domains.HeaderSlim,
	}
	if err := domains.Run(rc); err != nil {
		if stErr := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "build", Attempted: true, Required: buildRequired,
				Outcome: "failed", Reason: err.Error(),
			})
		}); stErr != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
		}
		return silentExit(err)
	}

	// Read v2 manifests to determine what was produced. PublishedCount =
	// successful docker push outcomes. Distinguish "not found" (no
	// publishable artifacts for this ref) from "unreadable" (genuine
	// error). Unreadable is NOT treated as "completed with no images" —
	// that would cause security to skip silently. Instead, Completed
	// stays false so downstream stages proceed and fail with diagnostic
	// errors.
	outputs, outErr := artifact.ReadOutputsManifest(rootDir)
	results, resErr := artifact.ReadResultsManifest(rootDir)

	bothNotFound := errors.Is(outErr, artifact.ErrOutputsManifestNotFound) &&
		errors.Is(resErr, artifact.ErrResultsManifestNotFound)

	switch {
	case outErr == nil && resErr == nil:
		// Count successful docker push outcomes via PublicationView.
		// Failed pushes surface in the view too — they don't count.
		views := artifact.BuildPublicationViews(outputs, results)
		count := 0
		for _, v := range views {
			if v.PushStatus == artifact.OutcomeSuccess {
				count++
			}
		}
		// Count images retained to the content store. Under transport, perform
		// RETAINS the built image to CAS and pushes nothing — distribution is the
		// publish phase's job. Such an artifact is unequivocally PRODUCED (it is the
		// exact bytes review scans and publish promotes), even though PublishedCount
		// is 0. ProducedImages must reflect production, not publication, or review's
		// pre-flight gate skips the scan whenever transport is active — exactly when
		// the carried layout is the only scannable target. (Produced != published.)
		retained := 0
		for _, a := range outputs.Artifacts {
			if a.Kind == "docker" && a.Persistence.Kind == artifact.PersistenceOCILayout && a.Persistence.OCILayout != nil {
				retained++
			}
		}
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.Build.ProducedImages = count > 0 || retained > 0
			st.Build.PublishedCount = count
			if count > 0 || retained > 0 {
				st.Build.ManifestPath = artifact.OutputsManifestPath
			}
			reason := ""
			if count == 0 && retained == 0 {
				reason = "manifests exist but no successful publications"
			}
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "build", Attempted: true, Completed: true, Required: buildRequired,
				Outcome: "success", Reason: reason,
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}

	case bothNotFound:
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.Build.ProducedImages = false
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "build", Attempted: true, Completed: true, Required: buildRequired,
				Outcome: "success", Reason: "no targets matched current ref",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}

	default:
		// One manifest is present but unreadable, or only one of the
		// pair exists. Either is a genuine error worth surfacing.
		var reason string
		switch {
		case outErr != nil && !errors.Is(outErr, artifact.ErrOutputsManifestNotFound):
			reason = fmt.Sprintf("outputs manifest unreadable: %v", outErr)
		case resErr != nil && !errors.Is(resErr, artifact.ErrResultsManifestNotFound):
			reason = fmt.Sprintf("results manifest unreadable: %v", resErr)
		default:
			reason = "outputs/results manifest pair inconsistent (one missing)"
		}
		fmt.Fprintf(os.Stderr, "warning: %s\n", reason)
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "build", Attempted: true, Required: buildRequired,
				Outcome: "failed", Reason: reason,
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}

	return nil
}

// ── deps runner ──────────────────────────────────────────────────────────────
// mapIgnores bridges config's DependencyIgnore (the .stagefreight.yml surface) to the
// dependency engine's VulnIgnore, keeping the engine free of a config-package dependency.
func mapIgnores(in []config.DependencyIgnore) []dependency.VulnIgnore {
	if len(in) == 0 {
		return nil
	}
	out := make([]dependency.VulnIgnore, 0, len(in))
	for _, ig := range in {
		out = append(out, dependency.VulnIgnore{ID: ig.ID, Reason: ig.Reason, Until: ig.Until})
	}
	return out
}

func depsRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)

	if !appCfg.Dependency.Enabled {
		// Deps disabled is an operator choice, not a failure — the subject is shippable.
		recordAuditionContract(rootDir, deriveAuditionContract(auditionInputs{RunnerHealthy: true, TestsPassed: true}))
		fmt.Println("  dependency update disabled in config")
		return nil
	}

	// The audition CONTRACT — the single authoritative record every downstream phase gates on
	// (Blocking) and narrate/publish/forge project (Replacement, Reason). Fail-closed: the zero
	// value blocks, so any early return below records a blocking contract unless a field was
	// explicitly cleared. The defer fires on EVERY exit path — no gate can be bypassed.
	in := auditionInputs{}
	defer func() { recordAuditionContract(rootDir, deriveAuditionContract(in)) }()

	if r := executorPreflight(rootDir, runner.Options{DockerRequired: false}); r.Health == runner.Unhealthy {
		return fmt.Errorf("deps subsystem: substrate unhealthy")
	}
	in.RunnerHealthy = true
	if err := runConfigPhase(rootDir); err != nil {
		return fmt.Errorf("deps subsystem: %w", err)
	}
	// Inspect → Classify → record into the audition contract. A Fatal finding (secret/conflict/
	// broken tree) voids the source: block and do not mutate. A Remediable finding (a freshness/
	// osv CVE) does NOT abort — it runs the deps update to produce a fix — but the SOURCE stays
	// Blocking, because the fix lands in a replacement commit (C′), not in this subject. Both are
	// Blocking in the contract; they differ only in whether a Replacement gets recorded below.
	lintFindings, _ := runUniversalLint(ctx, appCfg, rootDir, ciCtx.IsCI(), opts.Verbose)
	mut := lint.Classify(lintFindings)
	in.Fatal = mut.HasFatal()
	in.Remediable = mut.HasRemediable()
	if in.Fatal {
		return fmt.Errorf("deps subsystem (lint): source has %d fatal finding(s) — not mutating a void tree", len(mut.Fatal))
	}
	if in.Remediable {
		fmt.Printf("  deps: %d remediable finding(s) on the source — running the deps update to remediate\n", len(mut.Remediable))
	}

	// Correctness gate: run the tests on the COMMITTED tree — after lint, before any
	// dependency mutation ("is the committed tree healthy?"). A failed gating suite
	// fails audition here (withholds cistate → halts downstream). deps re-verifies
	// its own mutation separately (below) — it never trusts this run transitively.
	if err := auditionTests(ctx, appCfg, rootDir); err != nil {
		return err // in.TestsPassed stays false → the contract blocks
	}
	in.TestsPassed = true

	// Fetch security advisories from prior pipeline (cross-pipeline bridge).
	if ciCtx.IsCI() {
		ref := ciCtx.Branch
		if ref == "" {
			ref = ciCtx.DefaultBranch
		}
		fc, fcErr := newForgeClient(forge.Provider(ciCtx.Provider), ciCtx.RepoURL)
		if fcErr == nil {
			advisories, fetchErr := dependency.FetchAdvisories(ctx, fc, ref, rootDir)
			if fetchErr != nil {
				fmt.Fprintf(os.Stderr, "  deps: advisory fetch failed (continuing without): %v\n", fetchErr)
			} else if len(advisories) > 0 {
				fmt.Printf("  deps: fetched %d advisories from prior security scan\n", len(advisories))
			}
		}
	}

	// Run dependency update via the same code path as the CLI command
	result, err := runDependencyUpdateLogic(ctx, appCfg, rootDir, opts.Verbose)
	if err != nil {
		in.DepsErrored = true
		return fmt.Errorf("deps subsystem: %w", err)
	}

	// Structured output — same format as `stagefreight dependency update`
	w := os.Stdout
	color := output.UseColor()
	updateSec := output.NewSection(w, "Update", 0, color)

	appliedDeps := toOutputApplied(result.Applied)
	output.SectionApplied(updateSec, "Applied", appliedDeps, color)

	skippedGroups := aggregateSkippedItemized(result.Skipped)
	output.SectionSkippedItemized(updateSec, "Skipped", skippedGroups, color)

	cves := collectCVEsFixed(result.Applied)
	output.SectionCVEs(updateSec, cves, color)

	if result.Verified {
		status := "success"
		if result.VerifyErr != nil {
			status = "failed"
		}
		output.RowStatus(updateSec, "verify", "", status, color)
	}

	updateSec.Separator()
	updateSec.Row("%-16s%d", "applied", len(result.Applied))
	updateSec.Row("%-16s%d", "skipped", len(result.Skipped))
	updateSec.Row("%-16s%d", "files changed", len(result.FilesChanged))
	updateSec.Close()

	// Auto-commit if configured and files changed — gated by run_from.
	if appCfg.Dependency.Commit.Enabled && len(result.FilesChanged) > 0 {
		// Re-verify the MUTATED tree before landing the bump. Mutation is not trusted
		// transitively — the pre-deps correctness gate validated the committed tree,
		// not this graph change. A failed re-verify REJECTS the bump (no commit) but
		// does NOT halt the pipeline: the committed tree is still healthy. Same test
		// subsystem, one renderer/gate — renders as "Verify Upgrade".
		if ok, verr := test.Verify(ctx, appCfg, rootDir, os.Stdout, test.IntentDepReverify); verr != nil {
			fmt.Fprintf(os.Stderr, "  deps: re-verification error; not committing the update: %v\n", verr)
			return nil
		} else if !ok {
			fmt.Println("  deps: update rejected — it breaks the test suite; not committed")
			return nil
		}
		if rfResult := config.EvaluateRunFrom(appCfg.Dependency.Commit.RunFrom, ciCtx.RepoURL, config.PrimaryURL(appCfg)); !rfResult.Matched && rfResult.Mode != "ignore" {
			fmt.Fprintf(os.Stderr, "  deps commit: %s (%s)\n", rfResult.Mode, rfResult.Reason)
			return nil
		}
		// Compute bot branch for MR promotion mode
		var botBranch string
		if appCfg.Dependency.Commit.Promotion == config.PromotionMR && !ciCtx.IsCI() {
			fmt.Fprintf(os.Stderr, "warning: promotion \"mr\" requires CI context; falling back to direct mode\n")
		}
		if appCfg.Dependency.Commit.Promotion == config.PromotionMR && ciCtx.IsCI() {
			prefix := sanitizeBranchPrefix(appCfg.Dependency.Commit.MR.BranchPrefix)
			if prefix == "" {
				fmt.Fprintf(os.Stderr, "warning: invalid mr.branch_prefix %q; using default %q\n",
					appCfg.Dependency.Commit.MR.BranchPrefix, "stagefreight/deps")
				prefix = "stagefreight/deps"
			}
			shortSHA := ciCtx.SHA
			if len(shortSHA) > 8 {
				shortSHA = shortSHA[:8]
			}
			id := ciCtx.PipelineID
			if id == "" {
				id = shortSHA
			}
			botBranch = fmt.Sprintf("%s-%s-%s", prefix, id, shortSHA)
		}

		mode := "direct"
		if botBranch != "" {
			mode = "mr"
		}
		fmt.Printf("  deps: commit promotion mode: %s\n", mode)

		plannerOpts := commit.PlannerOptions{
			Type:    appCfg.Dependency.Commit.Type,
			Scope:   "deps",
			Message: appCfg.Dependency.Commit.Message,
			Paths:   result.FilesChanged,
			SkipCI:  boolPtr(appCfg.Dependency.Commit.SkipCI),
			Push:    boolPtr(appCfg.Dependency.Commit.Push),
		}
		if botBranch != "" {
			plannerOpts.Refspec = "HEAD:refs/heads/" + botBranch
			fmt.Printf("  deps: pushing to bot branch %s\n", botBranch)
		}

		commitResult, commitErr := autoCommitViaPlanner(ctx, appCfg, rootDir, plannerOpts)
		if commitErr != nil {
			fmt.Fprintf(os.Stderr, "warning: dependency auto-commit failed: %v\n", commitErr)
		}
		// Record the replacement commit as LINEAGE on the contract. It never changes Blocking —
		// a remediable source stays blocked whether or not the fix pushed; this only records
		// which commit (C′) supersedes this subject, for narrate / publish / the forge renderer.
		if commitResult != nil && commitResult.Pushed && !commitResult.NoOp {
			in.Replacement = commitResult.SHA
		}

		// MR mode: open merge request after successful push to bot branch
		if commitResult != nil && !commitResult.NoOp && commitResult.Pushed && botBranch != "" {
			target := appCfg.Dependency.Commit.MR.TargetBranch
			if target == "" {
				target = ciCtx.DefaultBranch
			}
			if target == "" {
				target = ciCtx.Branch
			}
			fc, fcErr := newForgeClient(forge.Provider(ciCtx.Provider), ciCtx.RepoURL)
			if fcErr != nil {
				fmt.Fprintf(os.Stderr, "warning: forge client init failed, cannot create MR: %v\n", fcErr)
			} else {
				commitSubject := strings.SplitN(commitResult.Message, "\n", 2)[0]
				mr, mrErr := fc.CreateMR(ctx, forge.MROptions{
					Title:        commitSubject,
					Description:  buildMRDescription(result),
					SourceBranch: botBranch,
					TargetBranch: target,
				})
				if mrErr != nil {
					fmt.Fprintf(os.Stderr, "warning: merge request creation failed: %v\n", mrErr)
				} else {
					fmt.Printf("  deps: opened merge request %s\n", mr.URL)
				}
			}
		}

		// Evaluate handoff only in direct mode — MR mode uses merge requests instead
		if botBranch == "" && commitResult != nil && !commitResult.NoOp && commitResult.Pushed {
			handoff := ci.EvaluateHandoff(ciCtx, appCfg.Dependency.CI.Handoff, commitResult.SHA)
			if msg := ci.FormatHandoffMessage(handoff); msg != "" {
				fmt.Println(msg)
			}
			if handoff.Decision == ci.HandoffRestart && ciCtx.PipelineID != "" {
				fc, fcErr := newForgeClient(forge.Provider(ciCtx.Provider), ciCtx.RepoURL)
				if fcErr == nil {
					if cancelErr := fc.CancelPipeline(ctx, ciCtx.PipelineID); cancelErr != nil {
						fmt.Fprintf(os.Stderr, "warning: pipeline cancel failed (freshness guards will handle): %v\n", cancelErr)
					}
				}
			}
			if handoff.Decision == ci.HandoffFail {
				return fmt.Errorf("deps subsystem: dependency repair at handoff depth %d — policy requires clean revision after handoff", handoff.Depth)
			}
		}
	}

	return nil
}

// runDependencyUpdateLogic runs the dependency update pipeline (resolve → filter → apply → verify → artifacts).
// Extracted from the Cobra command for reuse by CI runners.
func runDependencyUpdateLogic(ctx context.Context, appCfg *config.Config, rootDir string, isVerbose bool) (*dependency.UpdateResult, error) {
	w := os.Stdout

	// Load freshness options from config
	var freshnessOpts map[string]any
	if mc, ok := appCfg.Lint.Modules["freshness"]; ok {
		freshnessOpts = mc.Options
	}

	// Resolve ecosystems from config
	ecosystems := appCfg.Dependency.Scope.ScopeToEcosystems()

	// Collect files via lint engine
	output.SectionStart(w, "sf_deps_resolve", "Resolve")

	engine, err := lint.NewEngine(appCfg.Lint, rootDir, []string{"freshness"}, nil, isVerbose, nil)
	if err != nil {
		output.SectionEnd(w, "sf_deps_resolve")
		return nil, fmt.Errorf("creating lint engine: %w", err)
	}
	engine.ToolchainDesired = appCfg.Toolchains.Desired

	files, err := engine.CollectFiles()
	if err != nil {
		output.SectionEnd(w, "sf_deps_resolve")
		return nil, fmt.Errorf("collecting files: %w", err)
	}

	deps, err := freshness.ResolveDeps(ctx, freshnessOpts, files)
	if err != nil {
		output.SectionEnd(w, "sf_deps_resolve")
		return nil, fmt.Errorf("resolving dependencies: %w", err)
	}

	// Enrich dependencies with security scanner advisories from prior pipeline run.
	advisories, advErr := dependency.LoadAdvisories(rootDir)
	if advErr == nil && len(advisories) > 0 {
		details := dependency.EnrichDependencies(deps, advisories)
		if len(details) > 0 {
			fmt.Printf("  deps: enriched %d dependencies with security advisories\n", len(details))
			for _, d := range details {
				plural := "advisory"
				if d.Advisories != 1 {
					plural = "advisories"
				}
				fmt.Printf("    %-30s %-12s %d %s\n", d.Name, d.Version, d.Advisories, plural)
			}
		}
	}

	output.SectionEnd(w, "sf_deps_resolve")

	// Build update config
	outputDir := appCfg.Dependency.Output
	if outputDir == "" {
		outputDir = ".stagefreight/deps"
	}

	updateCfg := dependency.UpdateConfig{
		RootDir:    rootDir,
		OutputDir:  outputDir,
		DryRun:     false,
		Verify:     true,
		Vulncheck:  true,
		Ecosystems: ecosystems,
		Policy:     "all",
		Ignore:     mapIgnores(appCfg.Dependency.Ignore),
		Writer:     w, // render the Dependencies card alongside the other phase cards
	}

	result, err := dependency.Update(ctx, updateCfg, deps)
	if err != nil && result == nil {
		return nil, fmt.Errorf("dependency update: %w", err)
	}
	if err != nil {
		return result, fmt.Errorf("dependency update: %w", err)
	}

	return result, nil
}

// ── security runner ──────────────────────────────────────────────────────────
func securityRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	secAllowFailure := !appCfg.Security.IsRequired()
	rootDir := resolveWorkspace(ciCtx)

	// recordSecurity persists the security subsystem outcome (CI only). EVERY
	// terminal path records one: publish's authorization gate reads these raw
	// outcomes, so a path that returns without recording is invisible to it —
	// which is exactly how an unhealthy substrate used to let publish proceed
	// (the job failed, but no structured outcome existed for publish to deny on).
	recordSecurity := func(outcome, reason string) {
		if !ciCtx.IsCI() {
			return
		}
		if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
			s.RecordSubsystem(cistate.SubsystemState{
				Name: "security", Attempted: true, AllowFailure: secAllowFailure,
				Outcome: outcome, Reason: reason,
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}

	// Policy gate: skip non-release tags
	if ciCtx.IsTag() && !tagMatchesReleasePolicy(ciCtx.Tag, appCfg.Versioning) {
		fmt.Printf("  security: skipping — tag %q does not match any release tag source\n", ciCtx.Tag)
		recordSecurity("not_applicable", "tag does not match any release tag source")
		return nil
	}

	if !appCfg.Security.Enabled {
		fmt.Println("  security scan disabled in config")
		recordSecurity("not_applicable", "security scan disabled in config")
		return nil
	}

	// Terminal evaluation failure: an unhealthy substrate means the scan cannot
	// run, so the artifact is UNREVIEWED. Record it as a failure (not a silent
	// return) so publish's authorization gate denies distribution of bytes that
	// were never actually evaluated.
	if r := executorCheck(rootDir, runner.Options{DockerRequired: true}); r.Health == runner.Unhealthy {
		recordSecurity("failed", "substrate unhealthy — security scan could not run")
		return fmt.Errorf("security subsystem: substrate unhealthy")
	}

	// Pre-flight: check pipeline state for build output.
	// Only skip when build completed successfully and produced nothing.
	// Missing state = proceed (local dev, or state not written yet).
	// Build failed = proceed (let scan fail naturally with good error).
	if ciCtx.IsCI() {
		st, _ := cistate.ReadState(rootDir)
		buildSub := st.GetSubsystem("build")
		aud := st.GetSubsystem("audition")
		// Superseded/blocked subject: audition blocked it, so perform did not build and there is
		// nothing to review. Skip cleanly (the subject is reviewed on its follow-up pipeline)
		// rather than scanning a non-existent image and reporting a misleading failure.
		if buildSub == nil && aud != nil && aud.Blocking {
			reason := "audition blocked the subject — no artifact to review"
			if aud.Reason != "" {
				reason += " (" + aud.Reason + ")"
			}
			fmt.Printf("  security: skipping — %s\n", reason)
			if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
				s.RecordSubsystem(cistate.SubsystemState{
					Name: "security", Attempted: true, Skipped: true, AllowFailure: secAllowFailure,
					Outcome: "not_applicable", Reason: reason,
				})
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
			}
			return nil
		}
		if buildSub != nil && buildSub.Outcome == "success" && !st.Build.ProducedImages {
			reason := "build completed but produced no images"
			if buildSub.Reason != "" {
				reason += " (" + buildSub.Reason + ")"
			}
			fmt.Printf("  security: skipping — %s\n", reason)
			if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
				s.RecordSubsystem(cistate.SubsystemState{
					Name: "security", Attempted: true, Skipped: true, AllowFailure: secAllowFailure,
					Outcome: "not_applicable", Reason: reason,
				})
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
			}
			return nil
		}
	}

	// Mark attempted before running scan
	if ciCtx.IsCI() {
		if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
			s.RecordSubsystem(cistate.SubsystemState{
				Name: "security", Attempted: true, AllowFailure: secAllowFailure, Outcome: "failed",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}

	if err := RunSecurityScan(SecurityScanRequest{
		Ctx:       ctx,
		RootDir:   rootDir,
		Config:    appCfg,
		OutputDir: appCfg.Security.OutputDir,
		SBOM:      true,
		Writer:    os.Stdout,
	}); err != nil {
		if ciCtx.IsCI() {
			if stErr := cistate.UpdateState(rootDir, func(s *cistate.State) {
				s.RecordSubsystem(cistate.SubsystemState{
					Name: "security", Attempted: true, AllowFailure: secAllowFailure,
					Outcome: "failed", Reason: err.Error(),
				})
			}); stErr != nil {
				fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
			}
		}
		return fmt.Errorf("security subsystem: %w", err)
	}

	if ciCtx.IsCI() {
		if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
			s.RecordSubsystem(cistate.SubsystemState{
				Name: "security", Attempted: true, Completed: true, AllowFailure: secAllowFailure,
				Outcome: "success",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}

	return nil
}

// ── docs runner ──────────────────────────────────────────────────────────────
func docsRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	// Policy gate: skip non-release tags
	if ciCtx.IsTag() && !tagMatchesReleasePolicy(ciCtx.Tag, appCfg.Versioning) {
		fmt.Printf("  docs: skipping — tag %q does not match any release tag source\n", ciCtx.Tag)
		return nil
	}

	if !appCfg.Docs.Enabled {
		fmt.Println("  docs generation disabled in config")
		return nil
	}

	if !ci.IsBranchHeadFresh(ciCtx) {
		fmt.Println("  docs: skipping — pipeline SHA is not branch HEAD (newer pipeline will ship)")
		return nil
	}

	// Loop prevention: if the current commit was created by StageFreight's docs subsystem,
	// do not re-run docs. StageFreight recognizes its own output.
	// This is intelligence, not [skip ci] suppression.
	if isDocsAutoCommit(appCfg, ciCtx) {
		fmt.Println("  docs: skipping — current commit is a StageFreight docs auto-commit")
		return nil
	}

	rootDir := resolveWorkspace(ciCtx)

	if r := executorCheck(rootDir, runner.Options{DockerRequired: false}); r.Health == runner.Unhealthy {
		return fmt.Errorf("docs subsystem: substrate unhealthy")
	}

	// Resolve BUILD_STATUS from pipeline state — not hardcoded in skeleton.
	// Reads accumulated subsystem state; docs is always the last consumer.
	// Missing state = something failed upstream = default to failing (not unknown).
	if os.Getenv("BUILD_STATUS") == "" || os.Getenv("BUILD_STATUS") == "passing" {
		if st, err := cistate.ReadState(rootDir); err == nil {
			os.Setenv("BUILD_STATUS", st.PipelineStatus())
		} else {
			os.Setenv("BUILD_STATUS", "failing")
		}
	}

	gen := appCfg.Docs.Generators

	if gen.Badges {
		// Badges default on, but a project with none configured (e.g. a static site)
		// should skip, not fail — "nothing to generate" is not an error in the
		// automatic docs phase. The explicit `stagefreight badge generate` still errors.
		if !hasConfiguredBadges(appCfg) {
			fmt.Println("  docs: badges skipped — no badge items configured")
		} else if err := RunConfigBadges(appCfg, rootDir, nil, ""); err != nil {
			return fmt.Errorf("docs subsystem (badges): %w", err)
		}
	}

	if gen.ReferenceDocs {
		outDir := rootDir + "/docs/modules"
		if err := RunDocsGenerate(rootCmd, outDir); err != nil {
			return fmt.Errorf("docs subsystem (reference docs): %w", err)
		}
	}

	if gen.Narrator {
		if err := RunNarrator(appCfg, rootDir, false, opts.Verbose); err != nil {
			return fmt.Errorf("docs subsystem (narrator): %w", err)
		}
	}

	if gen.DockerReadme {
		if err := RunDockerReadme(ctx, appCfg, rootDir, false); err != nil {
			fmt.Fprintf(os.Stderr, "warning: docker readme sync failed: %v\n", err)
			// Non-fatal — registry sync may fail without credentials
		}
	}

	// Auto-commit if configured — gated by run_from policy.
	// GitLab CI checks out detached HEAD by default. The planner handles this
	// by constructing refspecs from CI_COMMIT_BRANCH/CI_COMMIT_REF_NAME to
	// push HEAD to the correct branch ref. No branch checkout needed.
	if appCfg.Docs.Commit.Enabled {
		rfResult := config.EvaluateRunFrom(appCfg.Docs.Commit.RunFrom, ciCtx.RepoURL, config.PrimaryURL(appCfg))
		switch {
		case !rfResult.Matched && rfResult.Mode == "exit":
			fmt.Fprintf(os.Stderr, "  docs commit: blocked (%s)\n", rfResult.Reason)
		case !rfResult.Matched && rfResult.Mode == "read-only":
			fmt.Fprintf(os.Stderr, "  docs commit: read-only (%s)\n", rfResult.Reason)
		default: // matched or ignore
			if _, err := autoCommitViaPlanner(ctx, appCfg, rootDir, commit.PlannerOptions{
				Type:    appCfg.Docs.Commit.Type,
				Message: appCfg.Docs.Commit.Message,
				Body:    "Narrator: StageFreight\nCue: docs/narrator",
				Paths:   appCfg.Docs.Commit.Add,
				SkipCI:  boolPtr(appCfg.Docs.Commit.SkipCI),
				Push:    boolPtr(appCfg.Docs.Commit.Push),
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: docs auto-commit failed: %v\n", err)
			}
		}
	}

	// Sync accessories (git mirror on push events — no release data).
	// Mirror push is idempotent — safe even when no repo mutation occurred.
	syncMirrors(ctx, appCfg)

	return nil
}

// isDocsAutoCommit detects if the current commit was created by StageFreight's docs subsystem.
// Uses Cue trailer for deterministic detection — not fuzzy message matching.
// Secondary guard (belt + suspenders). Primary loop prevention is deterministic output.
func isDocsAutoCommit(appCfg *config.Config, ciCtx *ci.CIContext) bool {
	workspace := resolveWorkspace(ciCtx)
	body := gitCommitBody(workspace, "HEAD")
	return hasTrailer(body, "Cue", "docs/narrator")
}

func gitCommitBody(repoDir, _ string) string {
	// rev is always "HEAD" at all current call sites.
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		diag.Debug(diag.Verbose(), "gitCommitBody: could not open repo at %s: %v", repoDir, err)
		return ""
	}
	head, err := repo.Head()
	if err != nil {
		diag.Debug(diag.Verbose(), "gitCommitBody: could not resolve HEAD: %v", err)
		return ""
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		diag.Debug(diag.Verbose(), "gitCommitBody: could not load HEAD commit: %v", err)
		return ""
	}
	return strings.TrimSpace(c.Message)
}

func hasTrailer(body, key, value string) bool {
	target := key + ": " + value
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// ── release runner ───────────────────────────────────────────────────────────
func releaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	relAllowFailure := !appCfg.Release.IsRequired()
	rootDir := resolveWorkspace(ciCtx)

	if r := executorCheck(rootDir, runner.Options{DockerRequired: false}); r.Health == runner.Unhealthy {
		return fmt.Errorf("release subsystem: substrate unhealthy")
	}

	// run_from gate — controls mutation authority for release.
	rfResult := config.EvaluateRunFrom(appCfg.Release.RunFrom, ciCtx.RepoURL, config.PrimaryURL(appCfg))
	if !rfResult.Matched && rfResult.Mode == "exit" {
		reason := fmt.Sprintf("run_from: exit (%s)", rfResult.Reason)
		renderReleaseSkip(ciCtx, releaseSkipDisabled, reason)
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "release", Attempted: true, Skipped: true, AllowFailure: relAllowFailure,
				Outcome: "skipped", Reason: reason,
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
		return nil
	}
	releaseReadOnly := !rfResult.Matched && rfResult.Mode == "read-only"
	if releaseReadOnly {
		fmt.Fprintf(os.Stderr, "  release: read-only mode (%s)\n", rfResult.Reason)
	}

	if !appCfg.Release.Enabled {
		renderReleaseSkip(ciCtx, releaseSkipDisabled, "release disabled in config")
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "release", Attempted: true, Skipped: true, AllowFailure: relAllowFailure,
				Outcome: "not_applicable", Reason: "release disabled in config",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
		return nil
	}

	if !ci.IsBranchHeadFresh(ciCtx) {
		renderReleaseSkip(ciCtx, releaseSkipNotHead, "pipeline SHA is not branch HEAD")
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "release", Attempted: true, Skipped: true, AllowFailure: relAllowFailure,
				Outcome: "skipped", Reason: "pipeline SHA is not branch HEAD",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
		return nil
	}

	tag := opts.Tag
	if tag == "" {
		tag = ciCtx.Tag
	}
	if tag == "" {
		// No CI tag, but a push may still trigger a release CHANNEL. Synthesize the
		// channel tag (e.g. dev-{sha8}) from a matching release target and proceed.
		// Synthesized LOCALLY — SF_CI_TAG is never set — so build/security runners'
		// IsTag() gates stay false; only the release runner treats this as releasable.
		tag = synthesizeChannelTag(appCfg, rootDir)
	}
	if tag == "" {
		// No tag = a forge release is simply not applicable to this build. Branch
		// pipelines are the common case, so rendering a full "nothing to release"
		// card here would contradict the Distribution card on EVERY non-tag run and
		// drown the one thing publish did do. Record the not-applicable state for
		// the pipeline summary, but emit no card — publish shows only Distribution.
		if ciCtx.IsCI() {
			if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
				st.RecordSubsystem(cistate.SubsystemState{
					Name: "release", Attempted: true, Skipped: true, AllowFailure: relAllowFailure,
					Outcome: "not_applicable", Reason: "no tag context",
				})
			}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
			}
		}
		return nil
	}

	// Policy gate: check if tag matches ANY release target's when conditions.
	// Uses the same target enumeration as RunReleaseCreate (collectTargetsByKind + targetWhenMatches).
	if !releaseTagMatchesAnyTarget(appCfg, tag) {
		reason := fmt.Sprintf("tag %q does not match any release tag source", tag)
		renderReleaseSkip(ciCtx, releaseSkipPolicyMismatch, reason)
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "release", Attempted: true, Skipped: true, AllowFailure: relAllowFailure,
				Outcome: "not_applicable", Reason: reason,
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
		return nil
	}

	// Tag matches — mark eligible and attempted before running
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.Release.Eligible = true
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "release", Attempted: true, AllowFailure: relAllowFailure, Outcome: "failed",
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}

	if err := RunReleaseCreate(ReleaseCreateRequest{
		Ctx:             ctx,
		RootDir:         rootDir,
		Config:          appCfg,
		Tag:             tag,
		Ref:             ciCtx.SHA, // mint a synthesized channel tag at the build commit
		SecuritySummary: appCfg.Release.SecuritySummary,
		RegistryLinks:   appCfg.Release.RegistryLinks,
		CatalogLinks:    appCfg.Release.CatalogLinks,
		ReadOnly:        releaseReadOnly,
		Writer:          os.Stdout,
		Verbose:         opts.Verbose,
	}); err != nil {
		if stErr := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "release", Attempted: true, AllowFailure: relAllowFailure,
				Outcome: "failed", Reason: err.Error(),
			})
		}); stErr != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
		}
		return fmt.Errorf("release subsystem: %w", err)
	}

	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name: "release", Attempted: true, Completed: true, AllowFailure: relAllowFailure,
			Outcome: "success",
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}

	// Sync mirrors — git + release reconciliation from primary.
	syncMirrors(ctx, appCfg)

	return nil
}

// releaseTagMatchesAnyTarget returns true if the tag matches at least one
// release target's when conditions. Uses the same target enumeration as
// releaseSkipCode identifies why a release was skipped.
type releaseSkipCode string

const (
	releaseSkipNoTag          releaseSkipCode = "no_tag_context"
	releaseSkipDisabled       releaseSkipCode = "disabled"
	releaseSkipNotHead        releaseSkipCode = "not_head"
	releaseSkipPolicyMismatch releaseSkipCode = "policy_mismatch"
)

// renderReleaseSkip renders a structured skip section for the release subsystem.
func renderReleaseSkip(ciCtx *ci.CIContext, code releaseSkipCode, reason string) {
	color := output.UseColor()
	sec := output.NewSection(os.Stdout, "Release", 0, color)
	sec.Row("%-14s%s", "status", "skipped")
	sec.Row("%-14s%s", "reason", reason)
	ref := ciCtx.Branch
	if ref == "" {
		ref = ciCtx.Tag
	}
	sec.Row("%-14s%s", "ref", ref)
	sec.Row("%-14s%s", "sha", shortSHA(ciCtx.SHA))
	tag := ciCtx.Tag
	if tag == "" {
		tag = "none"
	}
	sec.Row("%-14s%s", "tag", tag)
	sec.Row("%-14s%s", "result", releaseSkipResult(code))
	sec.Close()
}

// releaseSkipResult maps a skip code to a human-readable outcome.
func releaseSkipResult(code releaseSkipCode) string {
	switch code {
	case releaseSkipNoTag:
		return "nothing to release"
	case releaseSkipDisabled:
		return "release disabled"
	case releaseSkipNotHead:
		return "superseded by newer pipeline"
	case releaseSkipPolicyMismatch:
		return "no matching release policy"
	default:
		return "skipped"
	}
}

// RunReleaseCreate (collectTargetsByKind + targetWhenMatches).
// Returns true if no release targets have when constraints (backward compat).
func releaseTagMatchesAnyTarget(appCfg *config.Config, tag string) bool {
	releaseTargets := pipeline.CollectTargetsByKind(appCfg, "release")
	if len(releaseTargets) == 0 {
		return true // no release targets configured
	}

	// CRITICAL:
	// This map is ONLY for when.git_tags lookup on target conditions.
	//
	// versioning.tag_sources MUST remain an ORDERED SEARCH PATH for version
	// resolution. DO NOT reuse this map for version selection. Doing so
	// reintroduces global filtering and breaks the search-path invariant.
	//
	// If you find yourself thinking "I can share this map with gitver", stop
	// and re-read the INVARIANT comment at the top of gitver.DetectVersionWithOpts.
	tagPatternMap := tagPatternLookupForConditionsOnly(appCfg.Versioning.TagSources)

	hasConstraints := false
	for _, t := range releaseTargets {
		if config.TargetIsUnconditional(t) {
			continue
		}
		hasConstraints = true
		if targetWhenMatches(t, tag, tagPatternMap, appCfg.Matchers.Branches) {
			return true
		}
	}

	return !hasConstraints
}

// channelTagTarget returns the first kind:release target that declares a Tag
// pattern (a release channel) and whose when: matches the current CI environment.
// Used to detect a push-triggered release channel when there is no CI tag.
// Returns nil if none applies.
func channelTagTarget(appCfg *config.Config) *config.TargetConfig {
	for i := range appCfg.Targets {
		t := appCfg.Targets[i]
		if t.Kind != "release" || strings.TrimSpace(t.Tag) == "" {
			continue
		}
		if config.TargetMatchesEnv(t, appCfg) {
			return &appCfg.Targets[i]
		}
	}
	return nil
}

// synthesizeChannelTag resolves a channel target's Tag pattern (e.g. dev-{sha:8})
// to a concrete tag for a push-triggered release. Resolved LOCALLY and never
// exported to SF_CI_TAG, so other runners' tag gates are unaffected. Returns ""
// if no channel target applies or version detection fails.
func synthesizeChannelTag(appCfg *config.Config, rootDir string) string {
	t := channelTagTarget(appCfg)
	if t == nil {
		return ""
	}
	vi, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		return ""
	}
	resolved := gitver.ResolveTags([]string{t.Tag}, vi)
	if len(resolved) == 0 || resolved[0] == "" {
		return ""
	}
	return resolved[0]
}

// tagMatchesReleasePolicy returns true if the tag matches any git_tags policy
// pattern (stable or prerelease). Used to gate subsystem runners on tag events
// so rolling tags like "latest" don't trigger full builds/scans/docs.
//
// The skeleton defines generic CI event classes; StageFreight enforces
// repo-specific tag eligibility at runtime from .stagefreight.yml policy.
// tagPatternLookupForConditionsOnly flattens a tag_sources slice into an
// id → pattern map for the SOLE purpose of resolving target.when.git_tags
// references. The name is deliberately hostile: any reuse of this map
// for version selection would reintroduce global filtering and break the
// search-path invariant enforced by gitver.DetectVersionWithOpts.
//
// Do NOT:
//   - pass this map to gitver
//   - reuse it in detectVersion
//   - cache it at package scope
//   - rename it to something friendlier
//
// If you need a pattern lookup somewhere else in the codebase, build your
// own local map at the call site with the same CRITICAL guard comment.
// Keeping a second copy is cheaper than sharing one that tempts misuse.
func tagPatternLookupForConditionsOnly(sources []config.TagSourceConfig) map[string]string {
	m := make(map[string]string, len(sources))
	for _, ts := range sources {
		m[ts.ID] = ts.Pattern
	}
	return m
}

func tagMatchesReleasePolicy(tag string, versioning config.VersioningConfig) bool {
	if len(versioning.TagSources) == 0 {
		return true // no tag sources = all tags are eligible
	}
	for _, ts := range versioning.TagSources {
		if config.MatchPatterns([]string{ts.Pattern}, tag) {
			return true
		}
	}
	return false
}

// ── mirror sync ─────────────────────────────────────────────────────────────

// syncMirrors runs per-mirror sync: git mirror first, then release reconciliation.
// Both sync domains read from the primary — no data needs to be passed in.
// Mirror push is idempotent — safe to call even when no mutation occurred.
// Release sync is idempotent — existing releases are not recreated.
func syncMirrors(ctx context.Context, appCfg *config.Config) {
	syncMirrorsWithMode(ctx, appCfg, false)
}

func syncMirrorsWithMode(ctx context.Context, appCfg *config.Config, readOnly bool) {
	// Resolve mirrors from identity graph.
	mirrors, err := config.ResolveAllMirrors(appCfg.Repos, appCfg.Forges, appCfg.Vars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  sync: warning: could not resolve mirrors: %v\n", err)
		return
	}
	if len(mirrors) == 0 {
		return
	}

	worktree := config.PrimaryWorktree(appCfg)

	// Check if any mirror wants release sync — resolve primary releases once.
	var primaryReleases []forge.ReleaseInfo
	hasReleaseSyncMirror := false
	for _, m := range mirrors {
		if m.Sync.Releases {
			hasReleaseSyncMirror = true
			break
		}
	}
	if hasReleaseSyncMirror {
		primaryURL := config.PrimaryURL(appCfg)
		if primaryURL != "" {
			provider := forge.DetectProvider(primaryURL)
			primaryClient, clientErr := newForgeClient(provider, primaryURL)
			if clientErr == nil {
				rels, listErr := primaryClient.ListReleases(ctx)
				if listErr == nil {
					primaryReleases = rels
				} else {
					fmt.Fprintf(os.Stderr, "  sync: warning: could not list primary releases: %v\n", listErr)
				}
			} else {
				fmt.Fprintf(os.Stderr, "  sync: warning: could not create primary forge client: %v\n", clientErr)
			}
		}
	}

	hasDegraded := false

	for _, m := range mirrors {

		// 1. Git mirror (if enabled)
		if m.Sync.Git && readOnly {
			fmt.Printf("  sync: %s: [read-only] would mirror push\n", m.ID)
		} else if m.Sync.Git {
			result, err := stagefreightsync.MirrorPush(ctx, worktree, *m)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  sync: %s: mirror error: %v\n", m.ID, err)
				hasDegraded = true
				continue // skip artifact sync for this mirror
			}

			if result.Status == stagefreightsync.SyncSuccess {
				fmt.Printf("  sync: %s: mirror ✓ (%s)\n", m.ID, result.Duration.Truncate(100*time.Millisecond))
			} else {
				fmt.Fprintf(os.Stderr, "  sync: %s: mirror DEGRADED — %s: %s\n", m.ID, result.FailureReason, result.Message)
				hasDegraded = true
				continue
			}
		}

		// 2. Release reconciliation (if enabled).
		// Reads from primary, projects missing releases to mirror. Idempotent.
		if m.Sync.Releases && len(primaryReleases) > 0 {
			mirrorClient, err := forge.NewFromAccessory(m.Provider, m.BaseURL, m.Project, m.Credentials)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  sync: %s: release error: %v\n", m.ID, err)
				continue
			}
			mirrorReleases, err := mirrorClient.ListReleases(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  sync: %s: release list error: %v\n", m.ID, err)
				continue
			}
			mirrorTags := make(map[string]bool, len(mirrorReleases))
			for _, r := range mirrorReleases {
				mirrorTags[r.TagName] = true
			}

			created := 0
			for _, r := range primaryReleases {
				if mirrorTags[r.TagName] {
					continue
				}
				name := r.Name
				if name == "" {
					name = r.TagName
				}
				if readOnly {
					fmt.Printf("  sync: %s: [read-only] would project release %s\n", m.ID, r.TagName)
					created++
					continue
				}
				_, createErr := mirrorClient.CreateRelease(ctx, forge.ReleaseOptions{
					TagName:     r.TagName,
					Name:        name,
					Description: r.Description,
					Draft:       r.Draft,
					Prerelease:  r.Prerelease,
				})
				if createErr != nil {
					fmt.Fprintf(os.Stderr, "  sync: %s: release %s error: %v\n", m.ID, r.TagName, createErr)
				} else {
					created++
				}
			}
			if created > 0 {
				fmt.Printf("  sync: %s: release ✓ (%d projected)\n", m.ID, created)
			} else {
				fmt.Printf("  sync: %s: release ✓ (in sync)\n", m.ID)
			}
		}
	}

	if hasDegraded {
		fmt.Fprintf(os.Stderr, "\n  ⚠ DEGRADED REPLICATION: one or more mirrors failed\n")
	}
}

// LegacySyncOverlapsMirror returns true if a legacy sync target's provider
// is also declared as a mirror, meaning the mirror should take precedence.
// Exported for use in release_create.go where legacy sync targets are executed.
func LegacySyncOverlapsMirror(targetProvider string, appCfg *config.Config) bool {
	for _, r := range appCfg.Repos {
		if r.HasRole("mirror") {
			f := config.FindForgeByID(appCfg.Forges, r.Forge)
			if f != nil && f.Provider == targetProvider {
				return true
			}
		}
	}
	return false
}

// ── commit helpers ───────────────────────────────────────────────────────────

// autoCommitViaPlanner uses commit.BuildPlan + backend.Execute for auto-commit.
// Returns the commit result for callers that need to inspect it (e.g. handoff).
// Non-fatal — callers should log warnings on error.
func autoCommitViaPlanner(ctx context.Context, appCfg *config.Config, rootDir string, opts commit.PlannerOptions) (*commit.Result, error) {
	registry := commit.NewTypeRegistry(appCfg.Commit.Types)
	plan, err := commit.BuildPlan(opts, appCfg.Commit, registry, rootDir)
	if err != nil {
		return nil, fmt.Errorf("auto-commit plan: %w", err)
	}

	// Select backend from config — same logic as the commit CLI command.
	// Decision is explicit and deterministic: forge, git, or auto (CI → forge).
	var useForge bool
	switch appCfg.Commit.Backend {
	case "forge":
		useForge = true
	case "git":
		useForge = false
	case "":
		useForge = output.IsCI()
	default:
		return nil, fmt.Errorf("auto-commit: unknown backend %q", appCfg.Commit.Backend)
	}

	var backend commit.Backend
	if useForge {
		fc, branch, fErr := detectForgeForPush(rootDir, plan, appCfg)
		if fErr != nil {
			if appCfg.Commit.Backend == "forge" {
				return nil, fmt.Errorf("auto-commit: forge backend requested but detection failed: %w", fErr)
			}
			// Implicit forge (CI auto-detection) failed — fall back to git with warning.
			fmt.Fprintf(os.Stderr, "warning: forge backend auto-detection failed, falling back to git: %v\n", fErr)
			backend = &commit.GitBackend{RootDir: rootDir}
		} else {
			backend = &commit.ForgeBackend{
				RootDir:     rootDir,
				ForgeClient: fc,
				Branch:      branch,
			}
		}
	} else {
		backend = &commit.GitBackend{RootDir: rootDir}
	}

	result, err := backend.Execute(ctx, plan, appCfg.Commit.Conventional)
	if err != nil {
		return nil, fmt.Errorf("auto-commit execute: %w", err)
	}
	if result.NoOp {
		fmt.Println("  auto-commit: nothing to commit")
		return result, nil
	}
	fmt.Printf("  auto-commit: %s\n", result.SHA)
	return result, nil
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}

// ── MR description builder ───────────────────────────────────────────────────

// buildMRDescription formats a rich markdown description for dependency update MRs.
func buildMRDescription(result *dependency.UpdateResult) string {
	if result == nil {
		return "No dependency update result available.\n\n---\n\n> Automated by **StageFreight**\n"
	}

	var b strings.Builder

	cves := collectCVEsFixed(result.Applied)
	files := uniqueSortedStrings(result.FilesChanged)

	// --- Summary header ---
	b.WriteString("## Dependency Updates\n\n")

	if len(result.Applied) == 0 {
		b.WriteString("No dependency updates were applied.\n")
	} else {
		b.WriteString(fmt.Sprintf("**%s updated**  \n", pluralize(len(result.Applied), "dependency", "dependencies")))
		if len(cves) > 0 {
			b.WriteString(fmt.Sprintf("**%s fixed**  \n", pluralize(len(cves), "security advisory", "security advisories")))
		}
		if len(files) > 0 {
			b.WriteString(fmt.Sprintf("**%s modified**  \n", pluralize(len(files), "file", "files")))
		}
	}

	mrWriteDivider(&b)

	// --- Updated Dependencies table ---
	if len(result.Applied) > 0 {
		mrSection(&b, "\U0001F4E6 Updated Dependencies")
		b.WriteString("| Dependency | From | To | Type |\n")
		b.WriteString("|---|---:|---:|:---|\n")
		for _, u := range result.Applied {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				mrEscapeCell(u.Dep.Name),
				mrEscapeCell(u.OldVer),
				mrEscapeCell(u.NewVer),
				mrEscapeCell(u.UpdateType),
			))
		}
		mrWriteDivider(&b)
	}

	// --- Security Fixes table ---
	if len(cves) > 0 {
		mrSection(&b, "\U0001F510 Security Fixes")
		b.WriteString("| CVE | Severity | Fixed By |\n")
		b.WriteString("|---|:---:|---|\n")
		for _, c := range cves {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
				mrEscapeCell(c.ID),
				mrEscapeCell(c.Severity),
				mrEscapeCell(c.FixedBy),
			))
		}
		mrWriteDivider(&b)
	}

	// --- Files Changed ---
	if len(files) > 0 {
		mrSection(&b, "\U0001F4C2 Files Changed")
		for _, f := range files {
			b.WriteString(fmt.Sprintf("- %s\n", f))
		}
		mrWriteDivider(&b)
	}

	// --- Skipped Dependencies ---
	if len(result.Skipped) > 0 {
		type reasonGroup struct {
			reason string
			items  []dependency.SkippedDep
		}
		groupMap := make(map[string]*reasonGroup)
		var groupOrder []string
		for _, s := range result.Skipped {
			r := dependency.NormalizeSkipReason(s.Reason)
			g, ok := groupMap[r]
			if !ok {
				g = &reasonGroup{reason: r}
				groupMap[r] = g
				groupOrder = append(groupOrder, r)
			}
			g.items = append(g.items, s)
		}
		sort.Strings(groupOrder)

		b.WriteString(fmt.Sprintf("\n<details>\n<summary>Skipped Dependencies (%d)</summary>\n\n", len(result.Skipped)))
		for _, r := range groupOrder {
			g := groupMap[r]
			sort.Slice(g.items, func(i, j int) bool {
				return g.items[i].Dep.Name < g.items[j].Dep.Name
			})
			b.WriteString(fmt.Sprintf("#### %s\n", r))
			cap := 5
			for i, s := range g.items {
				if i >= cap {
					b.WriteString(fmt.Sprintf("- ... and %d more\n", len(g.items)-cap))
					break
				}
				b.WriteString(fmt.Sprintf("- %s %s\n", s.Dep.Name, s.Dep.Current))
			}
			b.WriteString("\n")
		}
		b.WriteString("</details>\n")
		mrWriteDivider(&b)
	}

	// --- Verification ---
	if result.Verified {
		if result.VerifyErr != nil {
			b.WriteString("\u274C Verification: failed\n")
		} else {
			b.WriteString("\u2705 Verification: passed\n")
		}
		mrWriteDivider(&b)
	}

	// --- Footer ---
	b.WriteString("> Automated by **StageFreight**\n")

	return b.String()
}

// mrSection writes a markdown section heading with correct blank-line spacing.
func mrSection(b *strings.Builder, title string) {
	b.WriteString("## " + title + "\n\n")
}

// mrWriteDivider writes a horizontal rule with correct blank-line spacing.
func mrWriteDivider(b *strings.Builder) {
	b.WriteString("\n---\n\n")
}

// mrEscapeCell escapes pipe characters and strips newlines for markdown table cells.
func mrEscapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// uniqueSortedStrings returns a deduplicated, sorted copy of ss.
func uniqueSortedStrings(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	cp := make([]string, len(ss))
	copy(cp, ss)
	sort.Strings(cp)
	out := cp[:1]
	for _, s := range cp[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// pluralize returns "N thing" or "N things" based on count.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// sanitizeBranchPrefix cleans a user-provided branch prefix for safety.
// Returns empty string if the input is invalid.
func sanitizeBranchPrefix(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.TrimPrefix(p, "refs/heads/")
	p = strings.Trim(p, "/")
	p = strings.TrimRight(p, "-")
	if p == "" || strings.Contains(p, "..") || strings.Contains(p, " ") {
		return ""
	}
	if strings.HasSuffix(p, ".lock") || strings.HasPrefix(p, "-") || strings.Contains(p, "@{") {
		return ""
	}
	return p
}

// ── validate runner ─────────────────────────────────────────────────────────
func validateRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, _ ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)

	if r := executorPreflight(rootDir, runner.Options{DockerRequired: false}); r.Health == runner.Unhealthy {
		return fmt.Errorf("validate subsystem: substrate unhealthy")
	}
	if err := runConfigPhase(rootDir); err != nil {
		return fmt.Errorf("validate subsystem: %w", err)
	}

	// GitOps manifest validation is the audition readiness proof for a Flux repo.
	// It runs here directly — NOT as a lint module — for two reasons: its verdict
	// is a phase artifact that perform consumes (skip-invalid reconcile), so there
	// must be a single producer of a single verdict; and it must run independently
	// of whether generic file-linting is configured. Content-gated (inert with no
	// Flux resources).
	valErr := runFluxValidation(ctx, appCfg, rootDir)

	// Generic file-lint stays opt-in via lint.level. Thread the run ctx (carrying the
	// tool ledger) into runLint via the command, the same cmd.SetContext convention
	// reconcileRunner uses — so osv records and the lint Staged Tools box populates.
	var lintErr error
	if strings.TrimSpace(string(appCfg.Lint.Level)) != "" {
		cmd := &cobra.Command{}
		cmd.SetContext(ctx)
		lintErr = runLint(cmd, []string{})
	}

	if valErr != nil {
		return valErr
	}
	if lintErr != nil {
		return lintErr
	}
	// Correctness gate (after validation + lint). No-op for pure-manifest repos with
	// no testable builds; runs the suites for gitops repos that also carry code.
	testErr := auditionTests(ctx, appCfg, rootDir)
	return testErr
}

// runFluxValidation validates the repository's Flux manifests, persists the
// per-Kustomization verdicts as audition proof results (the single source of
// truth perform later consumes), and renders the outcome. Advisory: a failing
// verdict returns an error so the audition job surfaces it (allow_failure keeps
// the pipeline moving).
func runFluxValidation(ctx context.Context, appCfg *config.Config, rootDir string) error {
	start := time.Now()
	verdicts, meta, err := gitops.ValidateManifests(ctx, rootDir, appCfg.Toolchains.Desired)
	if err != nil {
		return fmt.Errorf("gitops validation: %w", err)
	}
	if len(verdicts) == 0 {
		return nil // no Flux content — inert
	}

	if werr := writeFluxProofResults(rootDir, verdicts, meta); werr != nil {
		fmt.Fprintf(os.Stderr, "warning: proof-results write failed: %v\n", werr)
	}
	provision.StageBox(ctx, os.Stdout, output.UseColor()) // Staged Tools box, in front of GitOps Validation
	renderFluxValidation(os.Stdout, start, verdicts, meta)

	failed := 0
	for _, v := range verdicts {
		if v.Status == gitops.Fail {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("gitops validation: %d kustomization(s) failed validation", failed)
	}
	return nil
}

// writeFluxProofResults maps gitops verdicts into the audition proof-results
// artifact, preserving any other proofs already recorded for this run.
func writeFluxProofResults(rootDir string, verdicts map[gitops.KustomizationKey]gitops.Verdict, meta *gitops.ValidationMeta) error {
	fv := &auditionproof.FluxValidate{
		Roots:    meta.Roots,
		Skipped:  meta.Skipped,
		Verdicts: make(map[string]auditionproof.Verdict, len(verdicts)),
		NoSchema: meta.NoSchema,
	}
	for key, v := range verdicts {
		findings := make([]auditionproof.Finding, 0, len(v.Findings))
		for _, f := range v.Findings {
			findings = append(findings, auditionproof.Finding{
				Severity: f.Severity.String(),
				Source:   f.Source,
				Message:  f.Message,
			})
		}
		fv.Verdicts[key.String()] = auditionproof.Verdict{Status: v.Status.String(), Findings: findings}
	}
	r, err := auditionproof.Read(rootDir)
	if err != nil {
		r = &auditionproof.Results{}
	}
	r.FluxValidate = fv
	return auditionproof.Write(rootDir, r)
}

func renderFluxValidation(w io.Writer, start time.Time, verdicts map[gitops.KustomizationKey]gitops.Verdict, meta *gitops.ValidationMeta) {
	color := output.UseColor()
	sec := output.NewSection(w, "GitOps Validation", time.Since(start), color)

	if meta.Skipped != "" {
		sec.Row("%-14s%s", "status", "skipped")
		sec.Row("%-14s%s", "reason", meta.Skipped)
		sec.Close()
		return
	}

	// Resource-level tallies. Validated conflates core- and catalog-checked, so it is
	// NOT claimed as "authoritative" — only reported as "schema-checked".
	validated, noSchemaResources := 0, 0
	for _, n := range meta.Validated {
		validated += n
	}
	for _, n := range meta.NoSchema {
		noSchemaResources += n
	}

	// Kustomization verdicts (authoritative: a Fail is core-schema / render / graph).
	failedKust := 0
	for _, v := range verdicts {
		if v.Status == gitops.Fail {
			failedKust++
		}
	}
	validKust := len(verdicts) - failedKust

	// Collect findings by authority in stable order. Fail = authoritative error;
	// Warn = heuristic advisory (community catalog).
	keys := make([]gitops.KustomizationKey, 0, len(verdicts))
	for kk := range verdicts {
		keys = append(keys, kk)
	}
	gitops.SortKeys(keys)
	var errs, advisories []gitops.Finding
	for _, kk := range keys {
		for _, f := range verdicts[kk].Findings {
			switch f.Severity {
			case gitops.Fail:
				errs = append(errs, f)
			case gitops.Warn:
				advisories = append(advisories, f)
			}
		}
	}

	totalResources := validated + noSchemaResources + len(errs)
	sec.Row("%-12s %d kustomizations · %d resources · %d roots", "scope", len(verdicts), totalResources, meta.Roots)

	// ── ✓ authoritative (verdict-level) / ✗ errors — success reads first ──
	if len(errs) == 0 {
		sec.Row("")
		sec.Row("%s %-20s %d/%d kustomizations valid · 0 errors",
			output.StatusIcon("success", color), "authoritative", validKust, len(verdicts))
	} else {
		sec.Row("")
		sec.Row("%s %-20s %d/%d kustomizations · %d error(s)",
			output.StatusIcon("failed", color), "errors", validKust, len(verdicts), len(errs))
		for _, f := range errs {
			sec.Row("   %s", schemaHeadline(f))
			if src := schemaSource(f); src != "" {
				sec.Row("   %s", output.Dimmed("  └ "+src, color))
			}
		}
	}

	// ── ⚠ heuristic advisories (community catalog, may be stricter than operator) ──
	if len(advisories) > 0 {
		sec.Row("")
		sec.Row("%s %-20s %d · community CRD catalog, may be stricter than your operator",
			output.StatusIcon("warning", color), "heuristic", len(advisories))
		for _, f := range advisories {
			sec.Row("   %s", schemaHeadline(f))
			if src := schemaSource(f); src != "" {
				sec.Row("   %s", output.Dimmed("  └ "+src, color))
			}
		}
	}

	// ── ○ schema unavailable — transparency, not a gap; grouped and compressed ──
	if len(meta.NoSchema) > 0 {
		sec.Row("")
		sec.Row("%s %-20s %d kinds · %d resources — no published schema",
			output.Dimmed("○", color), "schema unavailable", len(meta.NoSchema), noSchemaResources)
		sec.Row("   %s", compactKinds(meta.NoSchema))
		sec.Row("   %s", output.Dimmed("  └ structurally validated by kustomize build; operators validate on apply", color))
	}

	// ── result: a verdict, tying the tiers together ──
	sec.Separator()
	verdict := "PASS"
	if len(errs) > 0 {
		verdict = "FAIL"
	}
	summary := fmt.Sprintf("%d/%d valid", validKust, len(verdicts))
	if len(errs) > 0 {
		summary += fmt.Sprintf(" · %d error(s)", len(errs))
	}
	if len(advisories) > 0 {
		summary += fmt.Sprintf(" · %d advisory", len(advisories))
	}
	if noSchemaResources > 0 {
		summary += fmt.Sprintf(" · %d schema-unavailable", noSchemaResources)
	}
	sec.Row("%-12s %s · %s", "result", verdict, summary)
	sec.Close()

	// Escape hatch: the raw validator transcript (schema URL + jsonschema pointer +
	// original message) is demoted from the primary surface, never destroyed. In
	// GitLab it lands in a collapsed fold (one expand away); everywhere the audition
	// artifact retains the raw Message. Emitted only in GitLab so local runs stay clean.
	if output.IsGitLabCI() && len(errs)+len(advisories) > 0 {
		emitValidatorDetail(w, errs, advisories)
	}
}

// schemaHeadline renders a finding's interpreted, operator-side line — every part
// mechanically derived. Falls back to the raw message when the violation could not
// be parsed with confidence: truthful degradation over synthesized meaning.
func schemaHeadline(f gitops.Finding) string {
	s := f.Schema
	if s == nil {
		return f.Message // graph/render finding — already human-phrased
	}
	res := s.Kind + "/" + s.Name
	if !s.Parsed() {
		return fmt.Sprintf("%-24s %s", res, firstLine(s.Raw))
	}
	switch {
	case s.Field != "" && s.Rule != "":
		return fmt.Sprintf("%-24s %s — %s", res, s.Field, s.Rule)
	case s.Field != "":
		return fmt.Sprintf("%-24s %s", res, s.Field)
	default:
		return fmt.Sprintf("%-24s %s", res, s.Rule)
	}
}

// schemaSource is the compact authority attribution for a schema finding.
func schemaSource(f gitops.Finding) string {
	s := f.Schema
	if s == nil {
		return ""
	}
	authority := "datreeio CRD-catalog"
	if f.Source == "core-schema" {
		authority = "kubernetes core schema"
	}
	id := compactSchemaID(s.SchemaURL)
	if id == "" {
		id = strings.TrimSpace(s.Kind + " " + s.Version)
	}
	if id == "" {
		return authority
	}
	return authority + " · " + id
}

// compactSchemaID reduces a schema URL to its last two path segments without the
// .json suffix, e.g. ".../vault.banzaicloud.com/vault_v1alpha1.json" →
// "vault.banzaicloud.com/vault_v1alpha1". Empty URL yields "".
func compactSchemaID(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(url, ".json")
	parts := strings.Split(url, "/")
	if n := len(parts); n >= 2 {
		return parts[n-2] + "/" + parts[n-1]
	}
	return parts[len(parts)-1]
}

// compactKinds renders "no schema" kinds as a compressed, count-annotated list:
// "CRD·13  ExternalSecret·6  HelmRelease·3 ...", most-numerous first.
func compactKinds(m map[string]int) string {
	kinds := gitops.SortedKinds(m)
	sort.SliceStable(kinds, func(i, j int) bool { return m[kinds[i]] > m[kinds[j]] })
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s·%d", shortKind(k), m[k]))
	}
	return strings.Join(parts, "  ")
}

// shortKind abbreviates the one habitually-long kind; others pass through.
func shortKind(kind string) string {
	if kind == "CustomResourceDefinition" {
		return "CRD"
	}
	return kind
}

// firstLine returns the first line of s, trimmed — keeps a raw-fallback headline on
// one row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// emitValidatorDetail writes the raw validator transcript inside a GitLab collapsed
// fold — the demoted escape hatch for operators debugging weird CRD behavior.
func emitValidatorDetail(w io.Writer, errs, advisories []gitops.Finding) {
	output.SectionStartCollapsed(w, "gitops_validator_detail", "── validator transcript (raw) ────")
	emit := func(f gitops.Finding) {
		if f.Schema != nil {
			fmt.Fprintf(w, "    │ %s/%s: %s\n", f.Schema.Kind, f.Schema.Name, f.Schema.Raw)
			if f.Schema.SchemaURL != "" {
				fmt.Fprintf(w, "    │   schema: %s\n", f.Schema.SchemaURL)
			}
			return
		}
		fmt.Fprintf(w, "    │ %s\n", f.Message)
	}
	for _, f := range errs {
		emit(f)
	}
	for _, f := range advisories {
		emit(f)
	}
	output.SectionEnd(w, "gitops_validator_detail")
}

// advisorySuffix appends a non-gating advisory count to the result line, e.g.
// ", 2 advisory" — surfacing heuristic warnings without implying they failed.
func advisorySuffix(warned int) string {
	if warned == 0 {
		return ""
	}
	return fmt.Sprintf(", %d advisory", warned)
}

// ── reconcile runner ────────────────────────────────────────────────────────
func reconcileRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	start := time.Now()

	hasGitOps := strings.TrimSpace(appCfg.GitOps.Cluster.Name) != ""
	hasGovernanceClusters := len(appCfg.Governance.Clusters) > 0
	hasGovernanceSource := governanceSourceConfigured(appCfg, ciCtx)

	if !hasGitOps && !hasGovernanceClusters {
		renderCISkip("Reconcile", start, "no reconcile target configured")
		return nil
	}

	rootDir := resolveWorkspace(ciCtx)

	if r := executorCheck(rootDir, runner.Options{DockerRequired: false}); r.Health == runner.Unhealthy {
		return fmt.Errorf("reconcile subsystem: substrate unhealthy")
	}

	// GitOps reconcile — auth resolved at runtime (CA cert, OIDC, or kubeconfig).
	// No pre-flight gate — let the runtime detect available auth and fail
	// with a clear error if nothing works.
	if hasGitOps {
		cmd := &cobra.Command{}
		cmd.SetContext(ctx)
		if err := runReconcile(cmd, []string{}); err != nil {
			return err
		}
	}

	// Governance reconcile — requires clusters AND source resolvable.
	// Not mutually exclusive with gitops — both can run.
	// In CI, source is auto-resolved from CI context and apply is implicit.
	if hasGovernanceClusters {
		if !hasGovernanceSource {
			renderCISkip("Reconcile", start, "governance source not configured")
		} else {
			// CI implies --apply: the reconcile stage exists to apply.
			if err := executeGovernanceReconcile(ctx, GovernanceReconcileOpts{
				Apply:   true,
				Config:  appCfg,
				CICtx:   ciCtx,
				Verbose: opts.Verbose,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

// ── shared executor preflight ─────────────────────────────────────────────────

// executorCheck performs a silent substrate health assertion without rendering the
// Executor panel or writing cistate. Used by non-audition phases. Callers must
// inspect Health and return a subsystem-specific error on Unhealthy.
func executorCheck(rootDir string, opts runner.Options) runner.ExecutionReport {
	return runner.Run(rootDir, opts)
}

// executorPreflight runs substrate assessment, renders the Executor panel, and
// persists the report to cistate. Authoritative readiness discovery — belongs
// to audition only. Returns the report so callers can inspect Health.
func executorPreflight(rootDir string, opts runner.Options) runner.ExecutionReport {
	start := time.Now()
	report := runner.Run(rootDir, opts)
	pipeline.RenderExecutorSection(os.Stdout, report, opts, output.UseColor(), time.Since(start))
	if stErr := cistate.UpdateState(rootDir, func(st *cistate.State) { st.Runner = report }); stErr != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
	}
	return report
}

// ── universal lint + docs orchestration ─────────────────────────────────────

// runUniversalLint runs the pre-build lint and returns its findings alongside the gate
// error. Findings are surfaced so the audition can Classify them (Fatal vs Remediable);
// the gate error is unchanged — a caller that only cares whether lint blocked still checks
// the error exactly as before.
func runUniversalLint(ctx context.Context, appCfg *config.Config, rootDir string, isCI bool, verbose bool) ([]lint.Finding, error) {
	color := output.UseColor()
	if strings.TrimSpace(string(appCfg.Lint.Level)) == "" {
		col := trace.NewCollector()
		col.Decision("lint", "status", "skipped — not configured", "no lint level set", "config", trace.StatusSkipped)
		sec := output.NewSection(os.Stdout, "Lint", 0, color)
		for _, e := range col.ForDomain("lint") {
			sec.Row("%-16s%s", e.Key, e.RenderValue())
			col.MarkRendered(e)
		}
		sec.Close()
		return nil, nil
	}
	_, findings, err := pipeline.RunLint(ctx, appCfg, rootDir, isCI, color, verbose, os.Stdout)
	return findings, err
}

// renderSyncPanel renders the DomainSync panel for mirror push outcomes.
func renderSyncPanel(results []syncMirrorOutcome) {
	if len(results) == 0 {
		return
	}
	col := trace.NewCollector()

	degraded := false
	for _, r := range results {
		switch {
		case r.err != nil || r.degraded:
			degraded = true
			detail := ""
			if r.err != nil {
				detail = r.err.Error()
			} else if r.reason != "" {
				detail = string(r.reason)
			}
			col.SideEffect("sync", r.id, "failed", detail, "mirror-push", trace.StatusFail)
		case r.skipped:
			col.Decision("sync", r.id, "skipped (read-only)", "read-only mode", "mirror-push", trace.StatusSkipped)
		default:
			v := r.value
			if v == "" {
				v = "pushed (" + r.duration.Truncate(100*time.Millisecond).String() + ")"
			}
			col.SideEffect("sync", r.id, v, "", "mirror-push", trace.StatusOK)
		}
	}
	if degraded {
		col.Decision("sync", "status", "degraded", "one or more mirrors failed", "sync-aggregate", trace.StatusWarn)
	} else {
		col.Decision("sync", "status", "success", "", "sync-aggregate", trace.StatusOK)
	}

	color := output.UseColor()
	sec := output.NewSection(os.Stdout, "Sync", 0, color)
	emissions := col.ForDomain("sync")
	for _, e := range emissions {
		icon := ""
		switch e.Status {
		case trace.StatusOK:
			icon = " " + output.StatusIcon("success", color)
		case trace.StatusWarn:
			icon = " " + output.StatusIcon("warning", color)
		case trace.StatusFail:
			icon = " " + output.StatusIcon("failed", color)
		}
		row := e.RenderValue()
		if e.Detail != "" {
			row += "  " + e.Detail
		}
		if e.Key == "status" {
			sec.Separator()
		}
		sec.Row("%-16s%s%s", e.Key, row, icon)
		col.MarkRendered(e)
	}
	sec.Close()

	if unrendered := col.Unrendered(); len(unrendered) > 0 {
		pipeline.RenderContractPanel(os.Stdout, unrendered, color)
	}
}

// syncMirrorOutcome captures the result of one mirror operation for panel rendering.
type syncMirrorOutcome struct {
	id       string
	degraded bool
	skipped  bool
	err      error
	reason   stagefreightsync.MirrorFailureReason
	duration time.Duration
	value    string // optional: overrides computed success value in renderSyncPanel
}

// ── shared CI skip renderer ─────────────────────────────────────────────────

// renderCISkip renders a structured skip section for any CI subsystem.
func renderCISkip(section string, start time.Time, reason string) {
	color := output.UseColor()
	sec := output.NewSection(os.Stdout, section, time.Since(start), color)
	sec.Row("%-14s%s", "status", "skipped")
	sec.Row("%-14s%s", "reason", reason)
	sec.Row("%-14s%s", "result", ciSkipResult(reason))
	sec.Close()
}

// governanceSourceConfigured checks if governance has a resolvable source.
func governanceSourceConfigured(appCfg *config.Config, ciCtx *ci.CIContext) bool {
	src, err := resolveGovernanceSourceFromOpts(GovernanceReconcileOpts{Config: appCfg, CICtx: ciCtx})
	return err == nil && src.RepoURL != ""
}

// ── config phase ─────────────────────────────────────────────────────────────

// runConfigPhase loads config, emits all config truth to a Collector, renders
// the Config panel exclusively from those emissions, and enforces the contract.
// Logs a warning if any emission was not rendered (hard fail in a future pass).
func runConfigPhase(rootDir string) error {
	_, report, _ := config.LoadWithReport(rootDir + "/.stagefreight.yml")

	col := trace.NewCollector()

	col.Public("config", trace.CategoryInput, "source", report.SourceFile, "config", trace.StatusOK)

	if len(report.Presets) > 0 {
		col.Public("config", trace.CategoryInput, "presets", strings.Join(report.Presets, " → "), "config", trace.StatusOK)
		if report.Overrides > 0 {
			col.Public("config", trace.CategoryInput, "overrides", fmt.Sprintf("%d", report.Overrides), "config", trace.StatusOK)
		}
	} else {
		col.Decision("config", "presets", "none", "no presets configured", "config", trace.StatusInfo)
	}

	var activeParts, inactiveParts []string
	for _, s := range report.Sections {
		if s.Active {
			activeParts = append(activeParts, s.Name+" "+configProvenanceIcon(s.Provenance))
		} else if s.Kind == "capability" {
			inactiveParts = append(inactiveParts, s.Name+" ⛔")
		}
	}
	if len(activeParts) > 0 {
		col.Public("config", trace.CategoryInput, "active", strings.Join(activeParts, "   "), "config", trace.StatusOK)
	}
	if len(inactiveParts) > 0 {
		col.Decision("config", "inactive", strings.Join(inactiveParts, "   "), "capability domains not configured", "config", trace.StatusInfo)
	}

	if report.VarsApplied > 0 {
		col.Public("config", trace.CategoryInput, "vars", fmt.Sprintf("%d applied", report.VarsApplied), "config", trace.StatusOK)
	}

	for i, w := range report.Warnings {
		col.PublicDetail("config", trace.CategoryDecision, fmt.Sprintf("warning_%d", i+1), w, w, "config", trace.StatusWarn)
	}

	resStatus := trace.StatusOK
	if report.Status == "partial" {
		resStatus = trace.StatusWarn
	} else if report.Status == "error" {
		resStatus = trace.StatusFail
	}
	col.Decision("config", "resolution", report.Status, "config resolution completeness", "resolver", resStatus)

	modelStatus := trace.StatusOK
	modelValue := report.Completeness
	if report.Completeness != "complete" {
		modelStatus = trace.StatusWarn
		modelValue = report.Completeness + " (resolution incomplete)"
	}
	col.Decision("config", "model", modelValue, "truth completeness signal", "resolver", modelStatus)

	color := output.UseColor()
	sec := output.NewSection(os.Stdout, "Config", 0, color)
	for _, e := range col.ForDomain("config") {
		icon := ""
		switch e.Status {
		case trace.StatusOK:
			icon = " " + output.StatusIcon("success", color)
		case trace.StatusWarn:
			icon = " " + output.StatusIcon("warning", color)
		case trace.StatusFail:
			icon = " " + output.StatusIcon("failed", color)
		}
		sec.Row("%-16s%s%s", e.Key, e.RenderValue(), icon)
		col.MarkRendered(e)
	}
	sec.Close()

	if unrendered := col.Unrendered(); len(unrendered) > 0 {
		fmt.Fprintf(os.Stderr, "warning: config panel: %d unrendered emissions\n", len(unrendered))
	}

	if report.Status == "error" {
		return fmt.Errorf("config resolution failed: %s", report.Error)
	}
	return nil
}

// configProvenanceIcon returns the display icon for a section's provenance.
func configProvenanceIcon(provenance string) string {
	switch provenance {
	case "manifest":
		return "📋"
	case "preset":
		return "♻️"
	default:
		return ""
	}
}

// ciSkipResult maps a skip reason to a human-readable outcome.
func ciSkipResult(reason string) string {
	switch reason {
	case "no validation configured":
		return "validation not configured"
	case "no reconcile target configured":
		return "nothing to reconcile"
	case "cluster auth unavailable":
		return "reconcile skipped — auth not available in this environment"
	case "governance source not configured":
		return "reconcile skipped — governance source not configured"
	default:
		return "skipped"
	}
}
