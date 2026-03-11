package cmd

import (
	"github.com/spf13/cobra"
)

var propsCmd = &cobra.Command{
	Use:   "props",
	Short: "Typed presentation items (badges, etc.)",
	Long: `Props is StageFreight's composable presentation subsystem.

Declarative, discoverable, validated, schema-aware presentation items.
Badges are the first prop format. Use 'props list' to see all available types.`,
}

func init() {
	rootCmd.AddCommand(propsCmd)
}
