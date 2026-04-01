package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/governance"

	"gopkg.in/yaml.v3"
)

var renderGated bool

var configRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Show the effective config after preset resolution",
	Long: `Renders the effective StageFreight config from .stagefreight.yml.

Without --gated: shows config after preset resolution (what config declares).
With --gated: shows runnable plan (what will actually execute after capability gating).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}

		config, err := loadConfig(rootDir)
		if err != nil {
			return err
		}

		if renderGated {
			fmt.Fprintln(os.Stderr, "# --gated: capability gating not yet implemented, showing config")
		}

		out, err := governance.RenderEffective(config)
		if err != nil {
			return fmt.Errorf("rendering config: %w", err)
		}

		fmt.Print(string(out))
		return nil
	},
}

func init() {
	configRenderCmd.Flags().BoolVar(&renderGated, "gated", false, "Show runnable plan after capability gating")
	configCmd.AddCommand(configRenderCmd)
}

// loadConfig loads .stagefreight.yml as a raw map.
func loadConfig(rootDir string) (map[string]any, error) {
	path := filepath.Join(rootDir, ".stagefreight.yml")
	return loadRawYAML(path)
}

// loadRawYAML loads a YAML file into a raw map.
func loadRawYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return raw, nil
}
