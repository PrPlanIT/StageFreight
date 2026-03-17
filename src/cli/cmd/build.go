package cmd

import (
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build artifacts (binaries, images)",
	Long:  "Build binaries, container images, and other artifacts.",
}

func init() {
	rootCmd.AddCommand(buildCmd)
}
