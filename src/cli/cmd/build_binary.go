package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	_ "github.com/PrPlanIT/StageFreight/src/build/contributors" // register build-strategy contributors
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/output"
)

var (
	bbLocal     bool
	bbPlatforms []string
	bbBuildID   string
	bbSkipLint  bool
	bbDryRun    bool
	bbOutputDir string
)

var buildBinaryCmd = &cobra.Command{
	Use:   "binary",
	Short: "Build Go binaries",
	Long: `Build Go binaries for configured platforms.

Compiles Go binaries using go build, cross-compiling for all configured platforms.
Injects version, commit, and build date via ldflags.`,
	RunE: runBuildBinaryDomains,
}

func init() {
	buildBinaryCmd.Flags().BoolVar(&bbLocal, "local", false, "build for current platform only")
	buildBinaryCmd.Flags().StringSliceVar(&bbPlatforms, "platform", nil, "override platforms (comma-separated)")
	buildBinaryCmd.Flags().StringVar(&bbBuildID, "build", "", "build specific entry by ID (default: all)")
	buildBinaryCmd.Flags().BoolVar(&bbSkipLint, "skip-lint", false, "skip pre-build lint gate")
	buildBinaryCmd.Flags().BoolVar(&bbDryRun, "dry-run", false, "show plan without executing")
	buildBinaryCmd.Flags().StringVar(&bbOutputDir, "output-dir", "", "override output directory")

	buildCmd.AddCommand(buildBinaryCmd)
}

// runBuildBinaryDomains is the standalone `build binary` entrypoint: a constrained
// invocation of the one lifecycle engine (domains.Run) with only the binary
// contributor active. Identity/Executor/Lint and the single Summary + manifest
// are owned by the run; the binary contributor supplies Detect/Plan/Build/Publish
// rows. --dry-run stops after Plan via the domain runner's dry-run gate.
func runBuildBinaryDomains(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	var outputs artifact.OutputsManifest
	rb := build.NewResultsBuilder()
	rc := &domains.RunContext{
		Ctx:       context.Background(),
		RootDir:   rootDir,
		Config:    cfg,
		Writer:    os.Stdout,
		Color:     output.UseColor(),
		Verbose:   verbose,
		SkipLint:  bbSkipLint,
		DryRun:    bbDryRun,
		Local:     bbLocal,
		Platforms: bbPlatforms,
		BuildID:   bbBuildID,
		Only:      []string{"binary"},
		Outputs:   &outputs,
		RB:        rb,
	}
	return domains.Run(rc)
}
