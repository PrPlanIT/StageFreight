package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

var rpDryRun bool

var releasePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune old releases using retention policy",
	Long: `Delete old releases on the detected forge using the retention
policy from the primary release target in .stagefreight.yml.

Alias templates from the release target are converted to patterns so
only releases matching the configured tag scheme are candidates.

Use --dry-run to preview what would be deleted without deleting.`,
	RunE: runReleasePrune,
}

func init() {
	releasePruneCmd.Flags().BoolVar(&rpDryRun, "dry-run", false, "show what would be deleted without deleting")

	releaseCmd.AddCommand(releasePruneCmd)
}

func runReleasePrune(cmd *cobra.Command, args []string) error {
	// Find the primary release target (no remote forge fields).
	primaryRelease := findPrimaryReleaseTarget(cfg)
	if primaryRelease == nil {
		return fmt.Errorf("no primary release target configured (targets[kind=release] without provider/url)")
	}

	if primaryRelease.Retention == nil || !primaryRelease.Retention.Active() {
		return fmt.Errorf("no retention policy configured on primary release target %q", primaryRelease.ID)
	}

	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	ctx := context.Background()
	color := output.UseColor()
	w := os.Stdout

	// Detect forge
	remoteURL, err := detectRemoteURL(rootDir)
	if err != nil {
		return fmt.Errorf("detecting remote: %w", err)
	}

	provider := forge.DetectProvider(remoteURL)
	if provider == forge.Unknown {
		return fmt.Errorf("could not detect forge from remote URL: %s", remoteURL)
	}

	forgeClient, err := newForgeClient(provider, remoteURL)
	if err != nil {
		return err
	}

	// Resolve tag patterns from alias templates (rolling git tag aliases)
	var patterns []string
	if len(primaryRelease.Aliases) > 0 {
		patterns = retention.TemplatesToPatterns(primaryRelease.Aliases)
	}

	if rpDryRun {
		return runReleasePruneDryRun(ctx, w, color, forgeClient, patterns, *primaryRelease.Retention)
	}

	start := time.Now()
	store := &forgeStore{forge: forgeClient}
	result, err := retention.Apply(ctx, store, patterns, *primaryRelease.Retention)
	elapsed := time.Since(start)

	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	// ── Retention section ──
	output.SectionStart(w, "sf_retention", "Retention")
	sec := output.NewSection(w, "Retention", elapsed, color)

	sec.Row("%-16s%d", "matched", result.Matched)
	sec.Row("%-16s%d", "kept", result.Kept)
	sec.Row("%-16s%d", "pruned", len(result.Deleted))

	for _, d := range result.Deleted {
		sec.Row("  - %s", d)
	}
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "error: %v\n", e)
	}

	sec.Close()
	output.SectionEnd(w, "sf_retention")

	return nil
}

func runReleasePruneDryRun(ctx context.Context, w *os.File, color bool, f forge.Forge, patterns []string, retentionPolicy config.RetentionPolicy) error {
	store := &forgeStore{forge: f}
	items, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("listing releases: %w", err)
	}

	// Filter by patterns
	var candidates []retention.Item
	for _, item := range items {
		if len(patterns) == 0 || matchesPatterns(patterns, item.Name) {
			candidates = append(candidates, item)
		}
	}

	if len(candidates) == 0 {
		fmt.Fprintln(w, "  no releases match the configured tag patterns")
		return nil
	}

	// Sort newest first
	sortItems(candidates)

	// Apply policies (dry run — just compute keep set)
	keepSet := retention.ApplyPolicies(candidates, retentionPolicy)

	var keepCount, pruneCount int

	// ── Retention (dry run) section ──
	output.SectionStart(w, "sf_retention", "Retention")
	sec := output.NewSection(w, "Retention (dry run)", 0, color)

	for i, item := range candidates {
		if keepSet[i] {
			keepCount++
			if verbose {
				sec.Row("  keep  %s", item.Name)
			}
		} else {
			pruneCount++
			sec.Row("  prune %s", item.Name)
		}
	}

	sec.Separator()
	sec.Row("matched=%d keep=%d prune=%d", len(candidates), keepCount, pruneCount)

	sec.Close()
	output.SectionEnd(w, "sf_retention")

	return nil
}

// forgeStore adapts forge.Forge to the retention.Store interface.
type forgeStore struct {
	forge forge.Forge
}

func (s *forgeStore) List(ctx context.Context) ([]retention.Item, error) {
	releases, err := s.forge.ListReleases(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]retention.Item, len(releases))
	for i, r := range releases {
		items[i] = retention.Item{
			Name:      r.TagName,
			CreatedAt: r.CreatedAt,
		}
	}
	return items, nil
}

func (s *forgeStore) Delete(ctx context.Context, name string) error {
	return s.forge.DeleteRelease(ctx, name)
}

// matchesPatterns is a thin wrapper for dry-run pattern matching.
func matchesPatterns(patterns []string, value string) bool {
	return config.MatchPatterns(patterns, value)
}

// sortItems sorts items newest-first.
func sortItems(items []retention.Item) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt.After(items[j-1].CreatedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
