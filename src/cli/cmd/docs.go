package cmd

import (
	"github.com/spf13/cobra"
)

// docsCmd is StageFreight-internal tooling: it reflects StageFreight's OWN Cobra
// tree and config structs to emit StageFreight's reference docs. It only makes sense
// inside the StageFreight repo (SF dogfoods itself — builds.reference runs it as a
// build step). It produces nothing useful in a downstream project, so it is Hidden
// from the user command surface. Other projects generate their own docs via their
// own `kind: command` build (e.g. `hasteward docs generate`).
var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "StageFreight-internal: generate StageFreight's own reference docs",
	Long: "StageFreight-internal reference-doc generation. Reflects StageFreight's own\n" +
		"command tree and config structs — meaningful only inside the StageFreight repo,\n" +
		"where it runs as a build step (builds.reference). Not intended for downstream use;\n" +
		"generate your project's docs with your own toolchain via a kind: command build.",
	Hidden: true,
}

func init() {
	rootCmd.AddCommand(docsCmd)
}
