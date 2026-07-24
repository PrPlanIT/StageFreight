package cmd

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/version"
	"github.com/spf13/cobra"
)

var versionVerbose bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Long:  "Print the version. --verbose adds build + runtime provenance (Go version, executable SHA-256, replay-guard capability) so a stale binary cannot masquerade as a guarded build.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if versionVerbose {
			fmt.Println(version.Verbose())
		} else {
			fmt.Println(version.String())
		}
		return nil
	},
}

func init() {
	versionCmd.Flags().BoolVar(&versionVerbose, "verbose", false, "show full build + runtime provenance")
	rootCmd.AddCommand(versionCmd)
}
