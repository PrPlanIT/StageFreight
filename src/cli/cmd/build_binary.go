package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	buildBinaryCmd.Flags().BoolVar(&bbDryRun, "dry-run", false, "show plan without executing")
	buildBinaryCmd.Flags().StringVar(&bbOutputDir, "output-dir", "", "also copy the built binaries here, as <os>-<arch>/<name> (they still land in "+build.DistDir+")")

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
		DryRun:    bbDryRun,
		Local:     bbLocal,
		Platforms: bbPlatforms,
		BuildID:   bbBuildID,
		Only:      []string{"binary"},
		Outputs:   &outputs,
		RB:        rb,
	}
	if err := domains.Run(rc); err != nil {
		return err
	}
	if bbOutputDir == "" || bbDryRun {
		return nil
	}
	exported, err := exportBinaries(rootDir, bbOutputDir, outputs.Artifacts)
	if err != nil {
		return err
	}
	for _, p := range exported {
		fmt.Fprintf(os.Stdout, "  exported %s\n", p)
	}
	return nil
}

// exportBinaries copies each built binary into dir as <os>-<arch>/<name>. The dist copy
// stays: release, sign and publish read it there.
func exportBinaries(rootDir, dir string, arts []artifact.Artifact) ([]string, error) {
	var exported []string
	for _, a := range arts {
		if a.Kind != "binary" || a.Binary == nil || a.Binary.Path == "" {
			continue
		}
		src := a.Binary.Path
		if !filepath.IsAbs(src) {
			src = filepath.Join(rootDir, src)
		}
		dest := filepath.Join(dir, a.Binary.OS+"-"+a.Binary.Arch, filepath.Base(src))
		if err := copyExecutableFile(src, dest); err != nil {
			return exported, fmt.Errorf("exporting %s to %s: %w", a.Name, dest, err)
		}
		exported = append(exported, dest)
	}
	if len(exported) == 0 {
		return nil, fmt.Errorf("--output-dir %s: the build produced no binaries to export", dir)
	}
	return exported, nil
}

// copyExecutableFile copies src to dest (creating dest's directory) with src's mode.
func copyExecutableFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
