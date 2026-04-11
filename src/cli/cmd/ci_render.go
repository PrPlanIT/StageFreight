package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/ci/render"
)

var (
	ciRenderWrite bool
	ciRenderCheck bool
)

var ciRenderCmd = &cobra.Command{
	Use:   "render <forge>",
	Short: "Render forge-native CI pipeline from .stagefreight.yml",
	Long: `Generate a forge-native CI pipeline file from StageFreight configuration.

Supported forges: gitlab

The rendered file is a committed generated artifact. StageFreight owns the
pipeline document — it is not hand-maintained.

Modes:
  --write   Write the rendered pipeline to the repo (e.g. .gitlab-ci.yml)
  --check   Verify the committed pipeline matches what would be rendered (exit 1 if stale)
  (default) Print the rendered pipeline to stdout`,
	Args: cobra.ExactArgs(1),
	RunE: runCIRender,
}

func init() {
	ciRenderCmd.Flags().BoolVar(&ciRenderWrite, "write", false, "write rendered pipeline to repo")
	ciRenderCmd.Flags().BoolVar(&ciRenderCheck, "check", false, "verify committed pipeline is up to date")

	ciCmd.AddCommand(ciRenderCmd)
}

func runCIRender(_ *cobra.Command, args []string) error {
	forge := args[0]

	p, err := render.Plan(cfg)
	if err != nil {
		return err
	}

	rendered, err := render.Emit(forge, p)
	if err != nil {
		return err
	}

	if ciRenderCheck {
		if err := render.Check(".", forge, rendered); err != nil {
			return err
		}
		target, _ := render.ForgeTarget(forge)
		fmt.Fprintf(os.Stderr, "%s is up to date\n", target)
		return nil
	}

	if ciRenderWrite {
		target, _ := render.ForgeTarget(forge)
		path := filepath.Join(".", target)
		if err := os.WriteFile(path, rendered, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", target)
		return nil
	}

	// Default: stdout
	_, err = os.Stdout.Write(rendered)
	return err
}
