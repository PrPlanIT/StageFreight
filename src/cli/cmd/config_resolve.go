package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/spf13/cobra"
)

var (
	resolveVerbose bool
	resolveOutput  string
)

var configResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Show how the effective config resolved, with provenance",
	Long: `Shows the resolved view of .stagefreight.yml — the config as the engine sees it:
- Which presets contributed, and how many local values overrode them
- Per-section provenance (manifest vs preset)
- With --verbose, per-value provenance (path, operation, source, overrides)

Resolution goes through the same loadResolved path builds use, so what this
prints is what runs — not a separate reporter-only view.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}
		path := filepath.Join(rootDir, ".stagefreight.yml")

		_, report, entries, err := config.LoadWithReport(path)
		if err != nil {
			return err
		}

		if resolveOutput == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}

		renderResolution(report, entries, resolveVerbose)
		return nil
	},
}

// renderResolution prints the resolved view as an output.Section card. This is the
// only renderer for the resolution surface; the config domain returns data
// (ConfigReport + entries) and never renders (render-boundary discipline). Later
// topology views (cred/sync) plug in as additional cards below this one.
func renderResolution(report config.ConfigReport, entries []config.MergeEntry, verbose bool) {
	color := output.UseColor()
	w := os.Stdout

	sec := output.NewSection(w, "Resolution", 0, color)
	sec.Row("%-16s%s", "source", report.SourceFile)
	sec.Row("%-16s%s", "status", report.Status)
	if len(report.Presets) > 0 {
		sec.Row("%-16s%s", "presets", strings.Join(report.Presets, ", "))
	} else {
		sec.Row("%-16s%s", "presets", "(none)")
	}
	sec.Row("%-16s%d", "local overrides", report.Overrides)
	sec.Row("%-16s%d", "vars applied", report.VarsApplied)

	// Active sections and where each came from (manifest vs preset).
	sec.Separator()
	active := 0
	for _, s := range report.Sections {
		if !s.Active {
			continue
		}
		active++
		sec.Row("%-16s%-10s%s", s.Name, s.Provenance, s.Kind)
	}
	if active == 0 {
		sec.Row("(no active sections)")
	}

	if len(report.Warnings) > 0 {
		sec.Separator()
		for _, warn := range report.Warnings {
			sec.Row("warning: %s", warn)
		}
	}
	sec.Close()

	// Per-value provenance is a verbose debugging aid — path, operation, source,
	// and whether a later layer overrode it.
	if verbose && len(entries) > 0 {
		sorted := make([]config.MergeEntry, len(entries))
		copy(sorted, entries)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

		vsec := output.NewSection(w, "Provenance (per value)", 0, color)
		for _, e := range sorted {
			line := fmt.Sprintf("%-36s %-8s %s", e.Path, e.Operation, e.Source)
			if e.Overridden {
				line += fmt.Sprintf("  (overridden by %s)", e.OverriddenBy)
			}
			vsec.Row("%s", line)
		}
		vsec.Close()
	}
}

func init() {
	configResolveCmd.Flags().BoolVarP(&resolveVerbose, "verbose", "v", false, "Show per-value provenance")
	configResolveCmd.Flags().StringVarP(&resolveOutput, "output", "o", "", "Output format: json")
	configCmd.AddCommand(configResolveCmd)
}
