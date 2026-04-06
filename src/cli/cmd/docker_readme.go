package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

var drDryRun bool

var dockerReadmeCmd = &cobra.Command{
	Use:   "readme",
	Short: "Sync README to container registries",
	Long: `Push README content to container registries that support description APIs.

Docker Hub receives both short (100-char) and full markdown descriptions.
Quay and Harbor receive short descriptions only.
Other registries are silently skipped.`,
	RunE: runDockerReadme,
}

func init() {
	dockerReadmeCmd.Flags().BoolVar(&drDryRun, "dry-run", false, "show prepared content without pushing")
	dockerCmd.AddCommand(dockerReadmeCmd)
}

// readmeSyncResult tracks the outcome of syncing to a single registry.
type readmeSyncResult struct {
	Registry string
	Status   string // "success" | "skipped" | "failed"
	Detail   string
	Err      error
}

// RunDockerReadme syncs README content to container registries.
// Extracted for reuse by both Cobra command and CI runners.
func RunDockerReadme(ctx context.Context, appCfg *config.Config, rootDir string, dryRun bool) error {
	// Collect docker-readme targets
	targets := pipeline.CollectTargetsByKind(appCfg, "docker-readme")
	if len(targets) == 0 {
		return fmt.Errorf("no docker-readme targets configured")
	}

	return runDockerReadmeImpl(ctx, appCfg, rootDir, dryRun, targets)
}

func runDockerReadme(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	if len(args) > 0 {
		rootDir = args[0]
	}
	ctx := context.Background()
	return RunDockerReadme(ctx, cfg, rootDir, drDryRun)
}

func runDockerReadmeImpl(ctx context.Context, appCfg *config.Config, rootDir string, dryRun bool, targets []config.TargetConfig) error {
	color := output.UseColor()
	w := os.Stdout

	// Resolve link bases from publish-origin repo role (once, shared across targets).
	linkBase, _ := config.ResolveLinkBase(appCfg)
	rawBase, _ := config.ResolvePublishOrigin(appCfg)

	// For dry-run, show content from the first target's file
	if dryRun {
		t := targets[0]
		resolvedDesc := gitver.ResolveVars(t.Description, appCfg.Vars)

		file := t.File
		if file == "" {
			file = "README.md"
		}
		content, err := registry.PrepareReadmeFromFile(file, resolvedDesc, linkBase, rawBase, rootDir)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Short description (%d chars):\n  %s\n\n", len(content.Short), content.Short)
		fmt.Fprintf(w, "Full description (%d bytes):\n%s\n", len(content.Full), content.Full)
		return nil
	}

	start := time.Now()
	var results []readmeSyncResult

	for _, t := range targets {
		// Resolve registry identity from identity graph or legacy inline fields.
		resolved, resolveErr := config.ResolveRegistryForTarget(t, appCfg.Registries, appCfg.Vars)
		if resolveErr != nil {
			results = append(results, readmeSyncResult{
				Registry: t.ID,
				Status:   "failed",
				Detail:   resolveErr.Error(),
				Err:      resolveErr,
			})
			continue
		}
		resolvedPath := resolved.Path
		resolvedDesc := gitver.ResolveVars(t.Description, appCfg.Vars)

		file := t.File
		if file == "" {
			file = "README.md"
		}

		content, err := registry.PrepareReadmeFromFile(file, resolvedDesc, linkBase, rawBase, rootDir)
		if err != nil {
			results = append(results, readmeSyncResult{
				Registry: resolved.URL + "/" + resolvedPath,
				Status:   "failed",
				Detail:   err.Error(),
				Err:      err,
			})
			continue
		}

		provider := resolved.Provider
		if provider == "" {
			provider = build.DetectProvider(resolved.URL)
		}

		// Only docker, github, quay, harbor support description APIs
		switch provider {
		case "docker", "dockerhub", "github", "quay", "harbor":
			// supported
		default:
			results = append(results, readmeSyncResult{
				Registry: resolved.URL + "/" + resolvedPath,
				Status:   "skipped",
				Detail:   "no description API",
			})
			continue
		}

		client, err := registry.NewRegistry(provider, resolved.URL, resolved.Credentials)
		if err != nil {
			results = append(results, readmeSyncResult{
				Registry: resolved.URL + "/" + resolvedPath,
				Status:   "failed",
				Detail:   err.Error(),
				Err:      err,
			})
			continue
		}

		// Per-target description override
		short := content.Short
		if resolvedDesc != "" {
			short = resolvedDesc
		}

		err = client.UpdateDescription(ctx, resolvedPath, short, content.Full)

		// Surface credential warnings (populated during auth)
		if warner, ok := client.(registry.Warner); ok {
			for _, warn := range warner.Warnings() {
				fmt.Fprintf(os.Stderr, "warning: %s/%s: %s\n", resolved.URL, resolvedPath, warn)
			}
		}

		if err != nil {
			if registry.IsForbidden(err) {
				results = append(results, readmeSyncResult{
					Registry: resolved.URL + "/" + resolvedPath,
					Status:   "skipped",
					Detail:   "forbidden (ensure PAT has read/write/delete scope)",
				})
				continue
			}
			results = append(results, readmeSyncResult{
				Registry: resolved.URL + "/" + resolvedPath,
				Status:   "failed",
				Detail:   err.Error(),
				Err:      err,
			})
			continue
		}

		results = append(results, readmeSyncResult{
			Registry: resolved.URL + "/" + resolvedPath,
			Status:   "success",
		})
	}

	elapsed := time.Since(start)

	// Tally
	var synced, skipped, errCount int
	for _, r := range results {
		switch r.Status {
		case "success":
			synced++
		case "skipped":
			skipped++
		case "failed":
			errCount++
		}
	}

	// ── README Sync section ──
	output.SectionStart(w, "sf_readme", "README Sync")
	sec := output.NewSection(w, "README Sync", elapsed, color)

	for _, r := range results {
		output.RowStatus(sec, r.Registry, r.Detail, r.Status, color)
	}

	sec.Separator()
	sec.Row("%d synced, %d skipped, %d errors", synced, skipped, errCount)

	sec.Close()
	output.SectionEnd(w, "sf_readme")

	if errCount > 0 {
		return fmt.Errorf("readme sync had %d error(s)", errCount)
	}
	return nil
}
