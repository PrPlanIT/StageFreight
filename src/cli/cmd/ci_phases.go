package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/ci/render"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// ── CI Lifecycle Phase Runners ───────────────────────────────────────────────
//
// These are the five canonical phase runners that form the public pipeline
// interface. GitLab (and any other CI provider) calls only these five commands:
//
//   stagefreight ci run audition
//   stagefreight ci run perform
//   stagefreight ci run review
//   stagefreight ci run publish
//   stagefreight ci run narrate
//
// Mode dispatch is owned here. lifecycle.mode in .stagefreight.yml determines
// which internal runner backs each phase. The YAML skeleton has no modality
// knowledge — it is a lifecycle-only transport.
//
// Phase authority:
//   audition — proves readiness (config, lint, runner panel, crucible)
//   perform  — authoritative production build or reconcile; no bootstrap semantics
//   review   — inspects perform output; not applicable for gitops/governance
//   publish  — sole phase authorized to distribute artifacts; not applicable for gitops/governance
//   narrate  — truth presentation; runs for all modes
//
// Internal runner names (deps, build, security, release, docs, validate,
// reconcile) remain as compatibility aliases in the registry.

// assertAuditionRan returns an error if cistate does not exist on disk.
// Called by every non-audition phase to enforce phase ordering. cistate is
// written by audition; its absence means audition did not run.
func assertAuditionRan(rootDir, phase string) error {
	p := filepath.Join(rootDir, cistate.StatePath)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return fmt.Errorf("missing cistate: audition must run before %s", phase)
	}
	return nil
}

// phaseNotApplicable renders a visible not_applicable result and records it
// in cistate. Used by review and publish for non-image lifecycle modes.
// Silent not_applicable is forbidden — users must see which phase did not run.
func phaseNotApplicable(rootDir, phase, mode string) error {
	start := time.Now()
	displayName := strings.ToUpper(phase[:1]) + phase[1:]
	reason := fmt.Sprintf("not applicable — lifecycle.mode=%q has no %s implementation", mode, phase)
	renderCISkip(displayName, start, reason)
	if stErr := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name:    phase,
			Outcome: "not_applicable",
			Reason:  fmt.Sprintf("lifecycle.mode=%q", mode),
		})
	}); stErr != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", stErr)
	}
	return nil
}

// renderAuditionBanner prints the full logo banner + code identity block. The
// audition phase is the sole place the logo appears — it is the readiness/proving
// phase at the head of the pipeline.
func renderAuditionBanner() {
	color := output.UseColor()
	output.Banner(os.Stdout, pipeline.IdentityInfo(), color)
	output.ContextBlock(os.Stdout, pipeline.CIContextKV(), color)
}

// renderPhaseIdentity prints the slim one-line provenance stamp (version · commit
// · branch) for a non-audition phase. Every job log carries its own identity so
// it is self-describing when read in isolation, without repeating the logo.
func renderPhaseIdentity() {
	output.IdentityLine(os.Stdout, pipeline.IdentityInfo(), output.UseColor())
}

// auditionPhaseRunner is the proving phase. Owns: CI freshness check,
// executorPreflight (full panel), runConfigPhase, lint, crucible bootstrap test.
// Dispatches to depsRunner for image mode or validateRunner for gitops/governance.
func auditionPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	// Full logo banner — audition is the only phase that shows it.
	renderAuditionBanner()

	// ── CI freshness check ─────────────────────────────────────────────
	// Verify the committed CI file matches what the current binary would
	// render. Runs before mode dispatch — CI freshness is universal.
	// Only enforced when running in CI (SF_CI_PROVIDER is set) and
	// ci.image is configured (render requires it).
	if ciCtx.IsCI() && appCfg.CI.Image != "" {
		rootDir := resolveWorkspace(ciCtx)
		if err := checkCIFreshness(ciCtx.Provider, rootDir, appCfg); err != nil {
			return fmt.Errorf("audition: %w", err)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return validateRunner(ctx, appCfg, ciCtx, opts)
	default: // "image" or "" — image is the default
		return depsRunner(ctx, appCfg, ciCtx, opts)
	}
}

// checkCIFreshness verifies the committed CI file matches what the current
// binary would render from .stagefreight.yml. Fails if stale or if the
// forge is not yet supported for rendering — no silent skipping.
func checkCIFreshness(forge, rootDir string, appCfg *config.Config) error {
	p, err := render.Plan(appCfg)
	if err != nil {
		return fmt.Errorf("ci freshness: %w", err)
	}

	rendered, err := render.Emit(forge, p)
	if err != nil {
		return fmt.Errorf("ci freshness: %w", err)
	}

	return render.Check(rootDir, forge, rendered)
}

// performPhaseRunner is the authoritative production build/reconcile phase.
// For image mode: official build only (no crucible, no bootstrap semantics).
// For gitops/governance: cluster reconcile.
func performPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "perform"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		// Reconcile binds to ACCEPTED state — a commit on the default branch, the
		// desired cluster state the controller converges to. Proposed intent (a
		// merge request, feature branch, or tag) must not be enacted; audition has
		// already validated it. Reconcile only when provably on the default branch.
		if !acceptedState(ciCtx) {
			renderCISkip("Perform", time.Now(), "reconcile binds to accepted state — not_applicable off the default branch (merge request / feature branch / tag)")
			return nil
		}
		// Reconcile has no build engine to stamp identity — render it here.
		// (The build path's stamp comes from the engine via HeaderSlim.)
		renderPhaseIdentity()
		return reconcileRunner(ctx, appCfg, ciCtx, opts)
	default:
		return buildRunner(ctx, appCfg, ciCtx, opts)
	}
}

// acceptedState reports whether the CI context represents accepted desired
// state — a commit on the default branch — the only state a controller should
// reconcile. The check is POSITIVE and fails closed: reconcile only when the
// pipeline is provably on the default branch; everything else (merge request,
// feature branch, tag, detached HEAD, or any context where the branch can't be
// confirmed) is treated as not-accepted and skipped, rather than risk enacting
// off-branch or unmerged state.
//
// This deliberately does NOT trust the event string: branch/event population
// varies by forge (GitLab reports "merge_request_event" and leaves
// CI_COMMIT_BRANCH empty on MR and tag pipelines; GitHub/Gitea use
// "pull_request"; Azure uses "PullRequest"). branch == default-branch is the one
// signal every provider reports consistently, and failing closed is the safe bias.
func acceptedState(ciCtx *ci.CIContext) bool {
	branch := strings.TrimSpace(ciCtx.Branch)
	def := strings.TrimSpace(ciCtx.DefaultBranch)
	return branch != "" && def != "" && branch == def
}

// reviewPhaseRunner inspects perform output. Not applicable for gitops/governance.
func reviewPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	renderPhaseIdentity()
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "review"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return phaseNotApplicable(rootDir, "review", mode)
	default:
		return securityRunner(ctx, appCfg, ciCtx, opts)
	}
}

// publishPhaseRunner is the sole phase authorized to distribute artifacts.
// Not applicable for gitops/governance.
func publishPhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	renderPhaseIdentity()
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "publish"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(appCfg.Lifecycle.Mode))
	switch mode {
	case "gitops", "governance":
		return phaseNotApplicable(rootDir, "publish", mode)
	default:
		// Authorization gate: publish externalizes irreversibly, so it must not
		// act unless the build produced the bytes AND review evaluated them. Read
		// the RAW recorded outcomes independent of the jobs' allow_failure — a
		// failed review blocks publish even though the review job is
		// allow_failure:true (scheduler continuation vs action authorization).
		// This lives INSIDE the image branch on purpose: gitops/governance have no
		// review→publish edge (their build/security subsystems never exist), so
		// authorizing against them would be a category error, not a transition.
		if err := authorizePhase(ciCtx, rootDir, "publish"); err != nil {
			return err
		}
		// Distribute content-store artifacts (digest-preserving promotion) before
		// release metadata. This is where image distribution happens in publish:
		// the bytes perform built and review verified are promoted to their
		// registry targets without rebuilding. No-op when transport produced
		// nothing to promote (the existing perform-time push remains the fallback
		// until promotion is proven and that path is removed).
		//
		// Mutation safety: promotion can move ROLLING registry tags (e.g.
		// latest-dev), and a stale pipeline moving those backward is the hazard.
		// Conservative policy for now: a superseded branch pipeline skips the
		// WHOLE promotion — including its immutable per-sha tags, which are not
		// themselves dangerous (that commit is moot) — via the same gate that
		// guards the retention prune below. A future refinement could let
		// freshness-safe immutable tags through while still blocking rolling
		// moves; today we take the simple, safe path. Tag pipelines are immutable
		// and exempt (ci.IsBranchHeadFresh returns true for tags).
		if !ci.IsBranchHeadFresh(ciCtx) {
			fmt.Fprintln(os.Stdout, "  publish: distribution skipped — pipeline SHA is not branch HEAD (a newer pipeline will ship)")
		} else if n, err := promoteArtifacts(ctx, appCfg, rootDir, os.Stdout); err != nil {
			return fmt.Errorf("publish promotion: %w", err)
		} else if n > 0 {
			// (The Distribution box already reports "N of N tag(s) published"; no
			// extra raw line here — keep the publish output cleanly boxed.)
			// Retire the content store: publish is its terminal reader, so once the
			// reviewed bytes are distributed the store's job is done. The CAS is a
			// workspace-scoped transient — RETIRED here by deterministic ownership,
			// not swept by a background GC. cas.Retire deletes only THIS pipeline's
			// store (never a concurrent project's). Non-fatal: the workspace wipe is
			// the backstop if it fails.
			if rErr := cas.Retire(rootDir); rErr != nil {
				fmt.Fprintf(os.Stderr, "warning: content store retire: %v\n", rErr)
			}
		}
		// Remote registry tag retention — publish is the only phase allowed to
		// mutate external distribution targets, and only here (post-push) is the
		// final remote tag set known. Local-daemon retention stays in perform.
		// Best-effort cleanup: a prune failure must not fail the pipeline.
		if ci.IsBranchHeadFresh(ciCtx) {
			if err := pruneRemoteRegistries(ctx, appCfg, rootDir, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "warning: registry retention: %v\n", err)
			}
		}

		if err := releaseRunner(ctx, appCfg, ciCtx, opts); err != nil {
			return err
		}
		// Generic package registry distribution (kind: generic-package) runs
		// alongside releases — no-op when no package target matches the event.
		return packagePublishRunner(ctx, appCfg, ciCtx, opts)
	}
}

// narratePhaseRunner renders truth from prior phase state. Runs for all modes.
func narratePhaseRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	renderPhaseIdentity()
	rootDir := resolveWorkspace(ciCtx)
	if err := assertAuditionRan(rootDir, "narrate"); err != nil {
		return err
	}
	return docsRunner(ctx, appCfg, ciCtx, opts)
}
