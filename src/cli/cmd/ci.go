package cmd

import (
	"github.com/spf13/cobra"
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "CI subsystem commands",
	Long: `Provider-neutral CI entry points.

Provider skeletons translate forge-native context into SF_CI_* environment
variables, then call stagefreight ci run <subsystem>. Subsystem behavior
is configured in .stagefreight.yml, not in provider files.`,
}

func init() {
	rootCmd.AddCommand(ciCmd)
}
