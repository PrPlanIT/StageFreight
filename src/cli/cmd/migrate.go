package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/config"
)

var (
	migrateInPlace bool
	migrateOutput  string
)

var migrateCmd = &cobra.Command{
	Use:   "migrate [file]",
	Short: "Migrate config to the latest schema version",
	Long: `Migrate a .stagefreight.yml config file to the latest schema version.

By default, prints the migrated config to stdout. Use --in-place to
overwrite the file, or --output to write to a different path.

Currently the latest schema version is 1. Future schema changes will
add migration steps here.

Note: The pre-version config format (before version: 1) is not supported
by this migration tool — it was an unversioned alpha that must be rewritten.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVarP(&migrateInPlace, "in-place", "i", false, "overwrite the config file in place")
	migrateCmd.Flags().StringVarP(&migrateOutput, "output", "o", "", "write migrated config to this path")

	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	inputPath := ".stagefreight.yml"
	if len(args) > 0 {
		inputPath = args[0]
	} else if cfgFile != "" {
		inputPath = cfgFile
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inputPath, err)
	}

	migrated, err := config.MigrateToLatest(data)
	if err != nil {
		return err
	}

	// Determine output destination.
	switch {
	case migrateInPlace:
		if err := os.WriteFile(inputPath, migrated, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", inputPath, err)
		}
		fmt.Fprintf(os.Stderr, "  migrated %s (in-place)\n", inputPath)

	case migrateOutput != "":
		if err := os.WriteFile(migrateOutput, migrated, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", migrateOutput, err)
		}
		fmt.Fprintf(os.Stderr, "  migrated %s → %s\n", inputPath, migrateOutput)

	default:
		// Print to stdout (pipeable).
		fmt.Print(string(migrated))
	}

	return nil
}
