package cmd

import (
	"github.com/spf13/cobra"
)

var scribeCmd = &cobra.Command{
	Use:   "scribe",
	Short: "Compose and inject content into markdown files",
	Long: `Scribe manages README sections using <!-- sf:<name> --> markers.

Compose badges, shields, text, and other modules into managed sections.
Content between markers is owned by StageFreight and replaced on each run.
Everything outside markers is never touched.`,
}

func init() {
	rootCmd.AddCommand(scribeCmd)
}
