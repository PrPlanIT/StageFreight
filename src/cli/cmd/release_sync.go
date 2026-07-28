package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/mirror"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/spf13/cobra"
)

var (
	relSyncDryRun bool
)

var releaseSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Converge mirror forges to the primary's releases (binaries included)",
	Long: `Converges every mirror whose sync block includes a releases facet toward
the primary forge's releases — carrying the notes AND re-hosting the attached
binaries, not just tag+notes shells.

Provenance-bounded and idempotent: an unchanged release is a no-op, a drifted
asset is replaced on its own, and a release SF did not place (a one-off, or one
another dev cut on the mirror) is left untouched. Pruning, when the facet opts in,
removes only SF-placed releases the primary no longer has.

Use --dry-run to preview the desired set without mutating any mirror.`,
	RunE: runReleaseSync,
}

func init() {
	releaseSyncCmd.Flags().BoolVar(&relSyncDryRun, "dry-run", false, "Preview only, do not create releases")
	releaseCmd.AddCommand(releaseSyncCmd)
}

func runReleaseSync(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if cfg == nil {
		return fmt.Errorf("no config loaded")
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return err
	}

	w := os.Stdout
	color := output.UseColor()

	// Detect primary forge.
	remoteURL, err := detectRemoteURL(rootDir)
	if err != nil {
		return fmt.Errorf("detecting remote: %w", err)
	}
	provider := forge.DetectProvider(remoteURL)
	primaryClient, err := newForgeClient(provider, remoteURL)
	if err != nil {
		return fmt.Errorf("creating primary forge client: %w", err)
	}

	// List primary releases.
	primaryReleases, err := primaryClient.ListReleases(ctx)
	if err != nil {
		return fmt.Errorf("listing primary releases: %w", err)
	}

	if len(primaryReleases) == 0 {
		fmt.Fprintln(w, "No releases found on primary forge.")
		return nil
	}

	start := time.Now()
	output.SectionStart(w, "sf_release_sync", "Release Sync")
	sec := output.NewSection(w, "Release Sync", 0, color)
	sec.Row("%-16s%s", "primary", string(provider))
	sec.Row("%-16s%d", "releases", len(primaryReleases))

	// Project to each mirror with sync.releases: true.
	var totalCreated, totalSkipped, totalFailed int

	resolvedMirrors, _ := config.ResolveAllMirrors(cfg.Repos, cfg.Forges, cfg.Vars)
	for _, m := range resolvedMirrors {
		if !m.Sync.SyncsReleases() {
			continue
		}

		mirrorClient, err := forge.NewFromAccessory(m.Provider, m.BaseURL, m.Project, m.Credentials)
		if err != nil {
			sec.Row("%s mirror:%s — %v", output.StatusIcon("failed", color), m.ID, err)
			totalFailed++
			continue
		}

		// Compute this mirror's desired release set from the primary — every
		// primary release, each with its file assets resolved for re-hosting —
		// then converge the mirror to it with the reconciler engine: binaries
		// carried (not notes-only), notes preserved, granular per-asset, and
		// provenance-bounded (only ever touches releases SF placed there).
		desired, err := mirror.BuildDesiredReleases(ctx, primaryClient, nil, config.RetentionPolicy{})
		if err != nil {
			sec.Row("%s mirror:%s — failed to build desired set: %v", output.StatusIcon("failed", color), m.ID, err)
			totalFailed++
			continue
		}
		// Recover prerelease across the mirror: GitLab (primary) has no native
		// prerelease field, so infer from the tag/notes.
		for i := range desired {
			if !desired[i].Prerelease {
				desired[i].Prerelease = resolveMirrorPrerelease(cfg, desired[i].Tag, desired[i].Body)
			}
		}

		prune := m.Sync.Releases != nil && m.Sync.Releases.Prune

		if relSyncDryRun {
			sec.Separator()
			sec.Row("%-16s%s (%d desired, prune=%v, dry-run)", "mirror", m.ID, len(desired), prune)
			totalSkipped += len(desired)
			continue
		}

		res, err := mirror.ReconcileReleases(ctx, primaryClient, mirrorClient, desired, mirror.Options{Prune: prune})
		if err != nil {
			sec.Row("%s mirror:%s — %v", output.StatusIcon("failed", color), m.ID, err)
			totalFailed++
			continue
		}

		sec.Separator()
		sec.Row("%-16s%s", "mirror", m.ID)
		for _, tag := range res.Created {
			sec.Row("  %s %s → %s/%s (created)", output.StatusIcon("success", color), tag, m.Provider, m.Project)
		}
		for _, tag := range res.Updated {
			sec.Row("  %s %s → %s/%s (updated)", output.StatusIcon("success", color), tag, m.Provider, m.Project)
		}
		for _, tag := range res.Pruned {
			sec.Row("  %s %s (pruned)", output.StatusIcon("success", color), tag)
		}
		// Foreign releases (no SF marker — a one-off or another dev's) are left
		// exactly as-is. Surfaced so a human can see what SF declined to manage.
		for _, tag := range res.SkippedForeign {
			sec.Row("  %s %s (foreign — left untouched)", output.StatusIcon("skipped", color), tag)
		}
		for _, e := range res.Errors {
			sec.Row("  %s %v", output.StatusIcon("failed", color), e)
		}
		if res.InSync > 0 {
			sec.Row("  %s %d already in sync", output.StatusIcon("success", color), res.InSync)
		}
		totalCreated += len(res.Created) + len(res.Updated) + len(res.Pruned)
		totalSkipped += res.InSync + len(res.SkippedForeign)
		totalFailed += len(res.Errors)
	}

	elapsed := time.Since(start)
	sec.Separator()
	mode := "apply"
	if relSyncDryRun {
		mode = "dry-run"
	}
	sec.Row("%-16s%s", "mode", mode)
	sec.Row("%-16s%d", "created", totalCreated)
	sec.Row("%-16s%d", "skipped", totalSkipped)
	sec.Row("%-16s%d", "failed", totalFailed)
	sec.Row("%-16s%s", "elapsed", elapsed.Round(time.Millisecond))
	sec.Close()
	output.SectionEnd(w, "sf_release_sync")

	if totalFailed > 0 {
		return fmt.Errorf("release sync completed with %d failures", totalFailed)
	}
	return nil
}
