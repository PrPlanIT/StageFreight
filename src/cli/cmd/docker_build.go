package cmd

import (
	"fmt"
	"github.com/PrPlanIT/StageFreight/src/cli/cliflag"
	"os"

	"github.com/PrPlanIT/StageFreight/src/build/docker"
	"github.com/spf13/cobra"
)

var (
	dbLocal     bool
	dbPlatforms []string
	dbTags      []string
	dbTarget    string
	dbBuildID   string
	dbDryRun    bool
	dbBuildMode string
)

var dockerBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build and push container images",
	Long: `Build container images using docker buildx.

Detects Dockerfiles, resolves tags from git, and pushes to configured registries.`,
	RunE: runDockerBuild,
}

func init() {
	dockerBuildCmd.Flags().BoolVar(&dbLocal, "local", false, "build for current platform, load into daemon")
	dockerBuildCmd.Flags().StringSliceVar(&dbPlatforms, "platform", nil, "override platforms (comma-separated)")
	dockerBuildCmd.Flags().StringSliceVar(&dbTags, "tag", nil, "override/add tags")
	dockerBuildCmd.Flags().StringVar(&dbTarget, "target", "", "override Dockerfile target stage")
	dockerBuildCmd.Flags().StringVar(&dbBuildID, "build", "", "build a specific entry by ID (default: all)")
	dockerBuildCmd.Flags().BoolVar(&dbDryRun, "dry-run", false, "show the plan without executing")
	cliflag.EnumVar(dockerBuildCmd.Flags(), &dbBuildMode, "build-mode", []string{"crucible"}, "", "build execution strategy (self-proving self-build)")

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

	if err := docker.Run(docker.Request{
		Context:    cmd.Context(),
		RootDir:    rootDir,
		Config:     cfg,
		Verbose:    verbose,
		Local:      dbLocal,
		Platforms:  dbPlatforms,
		Tags:       dbTags,
		Target:     dbTarget,
		BuildID:    dbBuildID,
		DryRun:     dbDryRun,
		BuildMode:  dbBuildMode,
		ConfigFile: cfgFile,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}); err != nil {
		return silentExit(err)
	}
	return nil
}
