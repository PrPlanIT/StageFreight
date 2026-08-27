package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

var (
	pruneOlderThan int
	pruneTool      string
	pruneKeepN     int
	pruneConfirm   bool
)

var toolchainPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old toolchain versions from cache",
	Long: `Manually invoke the toolchain-retention lifecycle: the DECLARED
toolchains.retention policy (keep_*, max_age, scoped refs:, protect:) plans which
installed versions fall out; flags override the declared policy for this invocation.

By default, shows what would be deleted (dry-run). Use --confirm to actually delete.

Safety:
  - Never prunes the version currently pinned in .stagefreight.yml
  - Versions without provenance metadata are left alone`,
	RunE: runToolchainPrune,
}

func init() {
	toolchainPruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 0, "override: also keep versions installed within the last N days (max_age)")
	toolchainPruneCmd.Flags().StringVar(&pruneTool, "tool", "", "filter output to a specific tool")
	toolchainPruneCmd.Flags().IntVar(&pruneKeepN, "keep-latest", 0, "override: keep the N most recent versions per tool (default: declared policy, engine default 2)")
	toolchainPruneCmd.Flags().BoolVar(&pruneConfirm, "confirm", false, "actually delete (default is dry-run)")

	toolchainCmd.AddCommand(toolchainPruneCmd)
}

func runToolchainPrune(_ *cobra.Command, _ []string) error {
	rootDir, _ := os.Getwd()
	installRoot := toolchain.InstallRoot(rootDir)

	// The declared policy, with per-invocation flag overrides.
	var policy config.RetentionPolicy
	pins := map[string]string{}
	if cfg != nil {
		policy = cfg.Toolchains.Retention
		for tool, pin := range cfg.Toolchains.Want {
			if pin.Constraint != "" {
				pins[tool] = pin.Constraint
			}
		}
	}
	if pruneKeepN > 0 {
		policy.KeepLast = pruneKeepN
	}
	if pruneOlderThan > 0 {
		policy.MaxAge = fmt.Sprintf("%dd", pruneOlderThan)
	}

	candidates, err := toolchain.PlanVersionRetention(installRoot, policy, pins)
	if err != nil {
		fmt.Printf("No toolchain cache at %s\n", installRoot)
		return nil
	}
	if pruneTool != "" {
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.Tool == pruneTool {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	if len(candidates) == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}

	if !pruneConfirm {
		fmt.Println("Dry run — would delete:")
		for _, c := range candidates {
			fmt.Printf("  %-14s %-14s %s (%s)\n", c.Tool, c.Version, c.InstalledAt.Format("2006-01-02"), c.Reason)
		}
		fmt.Printf("\n%d versions would be removed. Run with --confirm to delete.\n", len(candidates))
		return nil
	}

	deleted := 0
	for _, c := range candidates {
		if err := os.RemoveAll(c.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete %s: %v\n", c.Dir, err)
			continue
		}
		fmt.Printf("  deleted %-14s %s\n", c.Tool, c.Version)
		deleted++
	}
	fmt.Printf("\n%d versions removed.\n", deleted)
	return nil
}
