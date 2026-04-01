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
- Preset sources and what they contributed
- Source provenance for each value`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}

		config, err := loadConfig(rootDir)
		if err != nil {
			return err
		}

		// Resolve presets to build trace.
		_, entries, resolveErr := governance.ResolvePresets(config, nil, "local", ".stagefreight.yml", 0, nil)

		fmt.Printf("Config: .stagefreight.yml\n")
		fmt.Printf("  entries: %d\n", len(entries))
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "  resolve error: %v\n", resolveErr)
		}

		if resolveVerbose {
			fmt.Println()
			trace := governance.MergeTrace{Entries: entries}
			fmt.Print(governance.ExplainTrace(trace))
		}

		return nil
	},
}

func init() {
	configResolveCmd.Flags().BoolVarP(&resolveVerbose, "verbose", "v", false, "Show full resolution trace")
	configCmd.AddCommand(configResolveCmd)
}
