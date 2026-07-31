package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/scribe"
	"github.com/spf13/cobra"
)

var srOutput string

var scribeRenderCmd = &cobra.Command{
	Use:   "render <id>",
	Short: "Render one stencil's markdown",
	Long: `Resolve a single stencil (by id) and print its markdown fragment to
stdout, or write it to a file with --output.

Only stencils declared in .stagefreight.yml are renderable — the config is the source of
truth. To preview a producer type before declaring it, use 'stagefreight scribe types <type>'.`,
	Args: cobra.ExactArgs(1),
	RunE: runScribeRender,
}

func init() {
	scribeRenderCmd.Flags().StringVarP(&srOutput, "output", "o", "", "write markdown to a file instead of stdout")
	scribeCmd.AddCommand(scribeRenderCmd)
}

func runScribeRender(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	md, err := scribe.RenderContent(cfg, rootDir, args[0])
	if err != nil {
		return err
	}

	if srOutput == "" {
		fmt.Println(md)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(srOutput), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", srOutput, err)
	}
	if err := os.WriteFile(srOutput, []byte(md+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", srOutput, err)
	}
	fmt.Printf("  wrote %s\n", srOutput)
	return nil
}
