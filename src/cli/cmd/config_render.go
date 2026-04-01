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
	Short: "Show the fully merged effective config",
	Long: `Renders the effective StageFreight config after merging managed + local files.

Without --gated: shows merged config (what config declares).
With --gated: shows runnable plan (what will actually execute after capability gating).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}

		managed, local, err := loadTwoFileConfig(rootDir)
		if err != nil {
			return err
		}

		// Determine preset layer depth (0 for now — preset resolution not wired yet).
		managedLayer := 0
		merged, _ := governance.MergeConfigs(managed, local, managedLayer)

		if renderGated {
			// Capability gating not implemented yet — show merged with note.
			fmt.Fprintln(os.Stderr, "# --gated: capability gating not yet implemented, showing merged config")
		}

		out, err := governance.RenderEffective(merged)
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

// loadTwoFileConfig loads the managed and local config files as raw maps.
// Either or both may be absent — that's fine (Level 0 = local only).
func loadTwoFileConfig(rootDir string) (managed, local map[string]any, err error) {
	managedPath := filepath.Join(rootDir, ".stagefreight", "stagefreight-managed.yml")
	localPath := filepath.Join(rootDir, ".stagefreight.yml")

	managed, err = loadRawYAML(managedPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("loading managed config: %w", err)
	}

	local, err = loadRawYAML(localPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("loading local config: %w", err)
	}

	if managed == nil && local == nil {
		return nil, nil, fmt.Errorf("no config found (neither .stagefreight/stagefreight-managed.yml nor .stagefreight.yml)")
	}

	return managed, local, nil
}

// loadRawYAML loads a YAML file into a raw map.
// Returns nil map and os.ErrNotExist if file doesn't exist.
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
