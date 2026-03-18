package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/build/docker"
)

var (
	dbLocal     bool
	dbPlatforms []string
	dbTags      []string
	dbTarget    string
	dbBuildID   string
	dbSkipLint  bool
	dbDryRun    bool
	dbBuildMode string
)

var dockerBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build and push container images",
	Long: `Build container images using docker buildx.

Detects Dockerfiles, resolves tags from git, and pushes to configured registries.
Runs lint as a pre-build gate unless --skip-lint is set.`,
	RunE: runDockerBuild,
}

func init() {
	dockerBuildCmd.Flags().BoolVar(&dbLocal, "local", false, "build for current platform, load into daemon")
	dockerBuildCmd.Flags().StringSliceVar(&dbPlatforms, "platform", nil, "override platforms (comma-separated)")
	dockerBuildCmd.Flags().StringSliceVar(&dbTags, "tag", nil, "override/add tags")
	dockerBuildCmd.Flags().StringVar(&dbTarget, "target", "", "override Dockerfile target stage")
	dockerBuildCmd.Flags().StringVar(&dbBuildID, "build", "", "build a specific entry by ID (default: all)")
	dockerBuildCmd.Flags().BoolVar(&dbSkipLint, "skip-lint", false, "skip pre-build lint")
	dockerBuildCmd.Flags().BoolVar(&dbDryRun, "dry-run", false, "show the plan without executing")
	dockerBuildCmd.Flags().StringVar(&dbBuildMode, "build-mode", "", "build execution strategy: crucible (self-proving self-build)")

	dockerCmd.AddCommand(dockerBuildCmd)
}

func runDockerBuild(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	if len(args) > 0 {
		rootDir = args[0]
	}

	return docker.Run(docker.Request{
		Context:    cmd.Context(),
		RootDir:    rootDir,
		Config:     cfg,
		Verbose:    verbose,
		Local:      dbLocal,
		Platforms:  dbPlatforms,
		Tags:       dbTags,
		Target:     dbTarget,
		BuildID:    dbBuildID,
		SkipLint:   dbSkipLint,
		DryRun:     dbDryRun,
		BuildMode:  dbBuildMode,
		ConfigFile: cfgFile,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
}
