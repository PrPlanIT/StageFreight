package cmd

import (
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and manage StageFreight configuration",
	Long:  "Commands for inspecting resolved config, rendering effective config, and managing governance.",
}

func init() {
	rootCmd.AddCommand(configCmd)
}
