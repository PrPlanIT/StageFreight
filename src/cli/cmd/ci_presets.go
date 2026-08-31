package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// republishRefreshedPresets carries a satellite's own preset resolution back into the
// repo.
//
// Governance distributing presets is an optimization, not the only opportunity to
// resolve them: when that pipeline fails or is cancelled, the satellite still resolves
// its tracked references itself, and the retained copies on disk are already refreshed
// by the time config loads. Committing them here is what stops the repo describing a
// resolution that no longer matches what the run actually used — and it means a broken
// governance pipeline delays policy rather than freezing it.
//
// A pinned reference never reaches this: it is not fetched, so it cannot drift.
// presetRefreshCommit is the commit step, injectable so what this decides to commit can
// be asserted without driving git.
var presetRefreshCommit = autoCommitViaPlanner

func republishRefreshedPresets(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) {
	drifted := appCfg.DriftedPresets()
	if len(drifted) == 0 {
		return
	}

	refs := make([]string, 0, len(drifted))
	for _, o := range drifted {
		refs = append(refs, o.Ref.Raw)
	}
	sort.Strings(refs)

	fmt.Fprintf(os.Stdout, "  presets: %d tracked reference(s) moved since the retained copy:\n", len(refs))
	for _, r := range refs {
		fmt.Fprintf(os.Stdout, "    · %s\n", r)
	}

	// Nothing to carry back if the cache is not part of this repo.
	cacheDir := filepath.Join(rootDir, ".stagefreight", "preset-cache")
	if _, err := os.Stat(cacheDir); err != nil {
		fmt.Fprintln(os.Stdout, "  presets: no retained cache in this repo — nothing to republish")
		return
	}

	rf := config.EvaluateRunFrom(appCfg.Scribe.Commit.RunFrom, ciCtx.RepoURL, config.PrimaryURL(appCfg))
	if !rf.Matched && (rf.Mode == "exit" || rf.Mode == "read-only") {
		fmt.Fprintf(os.Stderr, "  presets: refresh not committed (%s)\n", rf.Reason)
		return
	}

	body := "The governance distribution did not carry these; this run resolved them from\n" +
		"their sources and is recording what it actually used.\n\nRefreshed:\n"
	for _, r := range refs {
		body += "  - " + r + "\n"
	}

	push := true
	if _, err := presetRefreshCommit(ctx, appCfg, rootDir, commit.PlannerOptions{
		Type:    "chore",
		Scope:   "presets",
		Message: "refresh tracked presets from their sources",
		Body:    body,
		Paths:   []string{".stagefreight/preset-cache"},
		// Marked as a governance-origin commit: it carries policy content, and the
		// marker is what keeps an automated commit from re-triggering the pipeline
		// that produced it.
		Origin: config.OriginGovernance,
		Push:   &push,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: preset refresh commit failed: %v\n", err)
	}
}
