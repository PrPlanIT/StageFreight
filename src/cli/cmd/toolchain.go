package cmd

import "github.com/spf13/cobra"

var toolchainCmd = &cobra.Command{
	Use:   "toolchain",
	Short: "Manage StageFreight toolchains",
	Long: `Inspect and manage the StageFreight toolchain cache.

StageFreight resolves external tools (Go, Trivy, Grype, etc.) at runtime:
downloaded, checksum-verified, cached, and executed by absolute path.

Subcommands:
  list    Show installed toolchain versions
  prune   Remove old toolchain versions from cache`,
}

func init() {
	rootCmd.AddCommand(toolchainCmd)
}
