package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/spf13/cobra"
)

var metadataCmd = &cobra.Command{
	Use:   "metadata",
	Short: "Push project identity to registries and forge repos",
	Long: `Run kind: metadata targets — push the project's identity (description, readme,
website, topics, logo) to every registry and forge repo destination that can hold it, from
one source of truth. Each field maps to the destination's capability and is skipped where
absent; nothing is truncated, and org/namespace fields are never touched.`,
	RunE: runMetadata,
}

func init() {
	rootCmd.AddCommand(metadataCmd)
}

func runMetadata(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	if len(args) > 0 {
		rootDir = args[0]
	}
	targets := pipeline.CollectTargetsByKind(cfg, "metadata")
	if len(targets) == 0 {
		return fmt.Errorf("no metadata targets configured")
	}
	postbuild.RunMetadataSection(context.Background(), os.Stdout, output.UseColor(), targets, rootDir, cfg)
	return nil
}
