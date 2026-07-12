package cmd

import (
	"fmt"
	"github.com/PrPlanIT/StageFreight/src/cli/cliflag"
	"os"

	"github.com/PrPlanIT/StageFreight/src/manifest"
	"github.com/spf13/cobra"
)

var (
	miSection string
	miFormat  string
	miBuildID string
)

var manifestInspectCmd = &cobra.Command{
	Use:   "inspect [manifest-path]",
	Short: "Pretty-print manifest or specific sections",
	Long: `Inspect reads a manifest JSON and displays it in human-readable format.

If no path is given, resolves the manifest from config and build ID.
Use --section to extract a specific dot-path (e.g., inventories.pip).
Use --format to control output: json, table, human (default: human).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runManifestInspect,
}

func init() {
	manifestInspectCmd.Flags().StringVar(&miSection, "section", "", "dot-path into manifest (e.g., inventories.pip)")
	cliflag.EnumVar(manifestInspectCmd.Flags(), &miFormat, "format", []string{"json", "table", "human"}, "human", "output format")
	manifestInspectCmd.Flags().StringVar(&miBuildID, "build-id", "", "resolve manifest for a specific build ID")

	manifestCmd.AddCommand(manifestInspectCmd)
}

func runManifestInspect(cmd *cobra.Command, args []string) error {
	var manifestPath string

	if len(args) > 0 {
		manifestPath = args[0]
	} else {
		// Resolve from config
		rootDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		buildID := miBuildID
		if buildID == "" {
			buildID = manifest.FindDefaultBuildID(cfg)
		}
		if buildID == "" {
			return fmt.Errorf("no build ID specified and no builds in config")
		}

		manifestPath = manifest.ResolveManifestPath(rootDir, cfg, buildID)
	}

	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	output, err := manifest.Inspect(m, manifest.InspectOptions{
		Section: miSection,
		Format:  miFormat,
	})
	if err != nil {
		return err
	}

	fmt.Print(output)
	return nil
}
