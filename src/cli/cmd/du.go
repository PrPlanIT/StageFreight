package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/disk"
)

var duJSON bool

var duCmd = &cobra.Command{
	Use:   "du",
	Short: "Report StageFreight's disk usage on this host",
	Long: "Report how much disk StageFreight is using: the persistent cache mount " +
		"(toolchain SDKs + Go/Rust build caches) and this workspace's .stagefreight/ " +
		"artifacts (content store, scan reports, release artifacts). Read-only — a `du` " +
		"scoped to the paths StageFreight owns, to find what is hogging space on a runner.",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		rep := disk.Scan(root)
		if duJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		renderDU(os.Stdout, rep)
		return nil
	},
}

func init() {
	duCmd.Flags().BoolVar(&duJSON, "json", false, "machine-readable JSON output")
	rootCmd.AddCommand(duCmd)
}

// humanBytes formats a byte count as a 1024-based human size (e.g. "8.1 GiB").
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func renderDU(w io.Writer, r disk.Report) {
	fmt.Fprintln(w, "StageFreight disk usage")
	fmt.Fprintln(w)
	if r.FS.Total > 0 {
		fmt.Fprintf(w, "  filesystem  %s  —  %s total, %s free (%d%% used)\n\n",
			r.FS.Path, humanBytes(r.FS.Total), humanBytes(r.FS.Free),
			pct(r.FS.Total-r.FS.Free, r.FS.Total))
	}
	if len(r.Groups) == 0 {
		fmt.Fprintln(w, "  no StageFreight caches or artifacts found here")
		return
	}
	for _, g := range r.Groups {
		renderEntry(w, g, 0)
	}
	fmt.Fprintln(w)
	note := ""
	if r.FS.Total > 0 {
		note = fmt.Sprintf("  (%d%% of %s)", pct(r.Total, r.FS.Total), r.FS.Path)
	}
	fmt.Fprintf(w, "  %-34s %10s%s\n\n", "total StageFreight footprint", humanBytes(r.Total), note)
	fmt.Fprintln(w, "  Caches are safe to delete (forces a one-time cold rebuild);")
	fmt.Fprintln(w, "  the content store is retired automatically by publish.")
}

// renderEntry prints one entry and its children, indented; the size column stays
// aligned across depths because indent padding + label width is held constant.
func renderEntry(w io.Writer, e disk.Entry, indent int) {
	pad := strings.Repeat("  ", indent)
	width := 34 - len(pad)
	name := e.Label
	if indent == 0 {
		name = e.Label + "  " + e.Path
		width = 34
		pad = ""
	}
	fmt.Fprintf(w, "  %s%-*s %10s\n", pad, width, name, humanBytes(e.Bytes))
	for _, c := range e.Children {
		if c.Bytes == 0 {
			continue
		}
		renderEntry(w, c, indent+1)
	}
}

func pct(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(part * 100 / whole)
}
