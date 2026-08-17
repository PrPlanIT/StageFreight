package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/scribe"
	"github.com/spf13/cobra"
)

var (
	nrDryRun bool
)

var scribeApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Reconcile stencils into files (badges + region injection)",
	Long: `Apply the scribe config: generate badge SVG artifacts, then render each
scribe.files region from the stencils it references.

Each files: entry names a marked region; its body embeds stencils by {id}
(with items: as sugar for a row of stencils). The rendered markdown is placed
between the region markers, replacing existing managed content idempotently.`,
	RunE: runScribeApply,
}

func init() {
	scribeApplyCmd.Flags().BoolVar(&nrDryRun, "dry-run", false, "preview changes without writing files")

	scribeCmd.AddCommand(scribeApplyCmd)
}

// RunScribe runs the scribe engine and renders its results. Extracted for reuse by
// both the Cobra command and CI runners. The engine (src/scribe) owns the domain work
// and returns data; this function is the sole renderer of that data.
func RunScribe(appCfg *config.Config, rootDir string, dryRun bool, isVerbose bool) error {
	start := time.Now()
	res, err := scribe.Run(appCfg, rootDir, dryRun, isVerbose)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	w := os.Stdout
	color := output.UseColor()

	// Dry-run: emit standalone output_file previews, then each region's would-be content.
	if dryRun {
		for _, p := range res.Previews {
			fmt.Fprintf(w, "# Would write to %s:\n%s\n", p.Path, p.Content)
		}
		for _, fr := range res.Files {
			if fr.Preview != "" {
				fmt.Fprintln(w, fr.Preview)
			}
		}
	}

	sec := output.NewSection(w, "Scribe", elapsed, color)
	var changed, unchanged int
	for _, fr := range res.Files {
		// Label by region id too: several regions target one file (README badges,
		// project, image, contents), so the path alone repeats indistinguishably.
		label := fr.File
		if fr.Region != "" {
			label = fmt.Sprintf("%s · %s", fr.File, fr.Region)
		}
		detail := fr.Detail
		if fr.Reason != "" {
			detail = fmt.Sprintf("%s (%s)", fr.Detail, fr.Reason)
		}
		output.RowStatus(sec, label, detail, fr.Status, color)
		switch fr.Detail {
		case "updated", "would update":
			changed++
		default:
			unchanged++
		}
	}
	sec.Separator()
	if dryRun {
		sec.Row("%d would update, %d unchanged", changed, unchanged)
	} else {
		sec.Row("%d updated, %d unchanged", changed, unchanged)
	}
	sec.Close()

	return nil
}

func runScribeApply(cmd *cobra.Command, args []string) error {
	if cfg.Scribe.IsZero() {
		fmt.Println("  scribe: nothing configured")
		return nil
	}
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Generate badge SVG artifacts first — they are side outputs the region content
	// references (![…](…/build.svg)). Skipped in dry-run (dry-run previews file changes,
	// not artifact writes).
	if !nrDryRun && hasConfiguredBadges(cfg) {
		if err := RunConfigBadges(cfg, rootDir, nil, ""); err != nil {
			return fmt.Errorf("scribe apply (badges): %w", err)
		}
	}

	if len(cfg.Scribe.Files) > 0 {
		if err := RunScribe(cfg, rootDir, nrDryRun, verbose); err != nil {
			return fmt.Errorf("scribe apply: %w", err)
		}
	}
	return nil
}
