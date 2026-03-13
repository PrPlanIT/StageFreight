package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var docsRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run all enabled documentation generators",
	Long: `Composed command that runs all enabled generators from docs config:
badges, reference docs, narrator, and docker readme.

Reads docs.generators in .stagefreight.yml to determine which generators
to run. This is the same logic used by 'stagefreight ci run docs'
(without auto-commit — use ci run docs for that).`,
	RunE: runDocsRun,
}

func init() {
	docsCmd.AddCommand(docsRunCmd)
}

func runDocsRun(cmd *cobra.Command, args []string) error {
	if !cfg.Docs.Enabled {
		fmt.Println("  docs generation disabled in config")
		return nil
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	gen := cfg.Docs.Generators
	ctx := context.Background()

	if gen.Badges {
		if err := RunConfigBadges(cfg, rootDir, nil, ""); err != nil {
			return fmt.Errorf("docs run (badges): %w", err)
		}
	}

	if gen.ReferenceDocs {
		outDir := filepath.Join(rootDir, "docs/modules")
		if err := RunDocsGenerate(rootCmd, outDir); err != nil {
			return fmt.Errorf("docs run (reference docs): %w", err)
		}
	}

	if gen.Narrator {
		if err := RunNarrator(cfg, rootDir, false, verbose); err != nil {
			return fmt.Errorf("docs run (narrator): %w", err)
		}
	}

	if gen.DockerReadme {
		if err := RunDockerReadme(ctx, cfg, rootDir, false); err != nil {
			fmt.Fprintf(os.Stderr, "warning: docker readme sync failed: %v\n", err)
			// Non-fatal — registry sync may fail without credentials
		}
	}

	return nil
}
