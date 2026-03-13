package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/internal/docsgen"
	"github.com/PrPlanIT/StageFreight/src/output"
)

var (
	dgOutputDir string
)

var docsGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate reference documentation from code",
	Long: `Generate CLI and config reference documentation as markdown fragments.

Output files are written to docs/modules/ and are designed to be assembled
into reference pages via narrator's kind: include.

Generated files:
  docs/modules/cli-reference.md     — CLI command reference from Cobra tree
  docs/modules/config-reference.md  — Config schema reference from Go structs`,
	RunE: runDocsGenerate,
}

func init() {
	docsGenerateCmd.Flags().StringVar(&dgOutputDir, "output-dir", "docs/modules", "output directory for generated fragments")

	docsCmd.AddCommand(docsGenerateCmd)
}

// RunDocsGenerate generates reference documentation from the Cobra command tree.
// Extracted for reuse by both Cobra command and CI runners.
// Requires rootCmd for CLI reference generation — pass it explicitly.
func RunDocsGenerate(rootCommand *cobra.Command, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	start := time.Now()
	color := output.UseColor()
	w := os.Stdout

	// Generate CLI reference from Cobra command tree.
	cliRef := docsgen.GenerateCLIReference(rootCommand)
	cliPath := filepath.Join(outputDir, "cli-reference.md")
	if err := os.WriteFile(cliPath, []byte(cliRef), 0o644); err != nil {
		return fmt.Errorf("writing CLI reference: %w", err)
	}

	// Generate config reference from struct metadata + overrides.
	configRef := docsgen.GenerateConfigReference()
	configPath := filepath.Join(outputDir, "config-reference.md")
	if err := os.WriteFile(configPath, []byte(configRef), 0o644); err != nil {
		return fmt.Errorf("writing config reference: %w", err)
	}

	elapsed := time.Since(start)
	sec := output.NewSection(w, "Docs Generate", elapsed, color)
	output.RowStatus(sec, "cli-reference.md", "generated", "success", color)
	output.RowStatus(sec, "config-reference.md", "generated", "success", color)
	sec.Close()

	return nil
}

func runDocsGenerate(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	outDir := dgOutputDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(rootDir, outDir)
	}

	return RunDocsGenerate(rootCmd, outDir)
}
