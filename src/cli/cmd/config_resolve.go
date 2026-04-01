package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/governance"
)

var resolveVerbose bool

var configResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Show the config resolution chain with provenance",
	Long: `Shows how the effective config was resolved:
- Which files were loaded (managed, local)
- How values were merged
- What overrode what
- Source provenance for each value`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}

		managed, local, err := loadTwoFileConfig(rootDir)
		if err != nil {
			return err
		}

		managedPresent := managed != nil
		localPresent := local != nil

		managedLayer := 0
		_, trace := governance.MergeConfigs(managed, local, managedLayer)

		// Print resolution summary.
		fmt.Print(governance.ExplainResolution(managedPresent, localPresent, trace))

		// Verbose: full trace.
		if resolveVerbose {
			fmt.Println()
			fmt.Print(governance.ExplainTrace(trace))
		}

		return nil
	},
}

func init() {
	configResolveCmd.Flags().BoolVarP(&resolveVerbose, "verbose", "v", false, "Show full merge trace")
	configCmd.AddCommand(configResolveCmd)
}
