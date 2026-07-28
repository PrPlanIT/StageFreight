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
	rdTag         string
	rdDryRun      bool
	rdSkipMirrors bool
)

var releaseDestroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy a release across the primary forge and its mirrors",
	Long: `Removes a release — the tag entry, notes, and attached assets — from the
primary forge and every release-sync mirror. The deliberate inverse of
'release create': it deletes the forge release objects, not the underlying git
tag, and does not resurrect (once gone from the source, a later mirror reconcile
has nothing to bring back).

Distinct from 'release prune', which is retention GC over a series. 'destroy'
removes one named release, everywhere, on purpose.

Use --dry-run to preview which forges would be affected.`,
	RunE: runReleaseDestroy,
}

func init() {
	releaseDestroyCmd.Flags().StringVar(&rdTag, "tag", "", "release tag to destroy (required)")
	releaseDestroyCmd.Flags().BoolVar(&rdDryRun, "dry-run", false, "preview affected forges without deleting")
	releaseDestroyCmd.Flags().BoolVar(&rdSkipMirrors, "skip-mirrors", false, "destroy on the primary forge only")
	releaseCmd.AddCommand(releaseDestroyCmd)
}

func runReleaseDestroy(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		return fmt.Errorf("no config loaded")
	}
	if rdTag == "" {
		return fmt.Errorf("--tag is required")
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return err
	}

	w := os.Stdout
	color := output.UseColor()

	// Primary forge (the source of truth we're removing from first).
	remoteURL, err := detectRemoteURL(rootDir)
	if err != nil {
		return fmt.Errorf("detecting remote: %w", err)
	}
	provider := forge.DetectProvider(remoteURL)
	primaryClient, err := newForgeClient(provider, remoteURL)
	if err != nil {
		return fmt.Errorf("creating primary forge client: %w", err)
	}

	// Labeled target list: primary + every release-sync mirror (unless skipped).
	type target struct {
		label  string
		client forge.Forge
	}
	targets := []target{{label: "primary:" + string(provider), client: primaryClient}}

	start := time.Now()
	output.SectionStart(w, "sf_release_destroy", "Release Destroy")
	sec := output.NewSection(w, "Release Destroy", 0, color)
	sec.Row("%-16s%s", "tag", rdTag)

	if !rdSkipMirrors {
		resolvedMirrors, _ := config.ResolveAllMirrors(cfg.Repos, cfg.Forges, cfg.Vars)
		for _, m := range resolvedMirrors {
			if !m.Sync.SyncsReleases() {
				continue
			}
			mc, err := forge.NewFromAccessory(m.Provider, m.BaseURL, m.Project, m.Credentials)
			if err != nil {
				sec.Row("%s mirror:%s — client error: %v", output.StatusIcon("failed", color), m.ID, err)
				continue
			}
			targets = append(targets, target{label: "mirror:" + m.ID, client: mc})
		}
	}

	sec.Separator()
	for _, t := range targets {
		sec.Row("%-16s%s", "target", t.label)
	}
	sec.Separator()

	if rdDryRun {
		sec.Row("%-16s%s", "mode", "dry-run")
		sec.Row("%-16s%d forge(s) would be affected", "would destroy", len(targets))
		sec.Close()
		output.SectionEnd(w, "sf_release_destroy")
		return nil
	}

	// Fan the delete across every target via the mirror engine. Errors are
	// collected, not fatal — one unreachable mirror must not block removal from
	// the rest.
	ds := make([]mirror.Destroyer, 0, len(targets))
	for _, t := range targets {
		ds = append(ds, t.client)
	}
	errs := mirror.DestroyRelease(ctx, rdTag, ds...)

	destroyed := len(targets) - len(errs)
	sec.Row("%-16s%s", "mode", "apply")
	sec.Row("%-16s%d", "destroyed", destroyed)
	sec.Row("%-16s%d", "failed", len(errs))
	for _, e := range errs {
		sec.Row("  %s %v", output.StatusIcon("failed", color), e)
	}
	sec.Row("%-16s%s", "elapsed", time.Since(start).Round(time.Millisecond))
	sec.Close()
	output.SectionEnd(w, "sf_release_destroy")

	if len(errs) > 0 {
		return fmt.Errorf("release destroy completed with %d failures", len(errs))
	}
	return nil
}
