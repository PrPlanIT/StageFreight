package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var docsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the narrate producers locally (badges + patches)",
	Long: `Runs the presence-enabled narrate producers from config — render badges and
apply marked-region patches to files — without committing. Same producer logic as
'stagefreight ci run narrate' (which also lands build trees + auto-commits).`,
	RunE: runDocsRun,
}

func init() {
	docsCmd.AddCommand(docsRunCmd)
}

func runDocsRun(cmd *cobra.Command, args []string) error {
	if cfg.Narrate.IsZero() {
		fmt.Println("  narrate: nothing configured")
		return nil
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	if len(cfg.Narrate.Badges) > 0 {
		if err := RunConfigBadges(cfg, rootDir, nil, ""); err != nil {
			return fmt.Errorf("narrate run (badges): %w", err)
		}
	}

	if len(cfg.Narrate.Patches) > 0 {
		if err := RunNarrator(cfg, rootDir, false, verbose); err != nil {
			return fmt.Errorf("narrate run (patches): %w", err)
		}
	}

	return nil
}
