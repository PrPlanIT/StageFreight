package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

var (
	pruneOlderThan int
	pruneTool      string
	pruneKeepN     int
	pruneConfirm   bool
)

var toolchainPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old toolchain versions from cache",
	Long: `Remove old toolchain versions from writable cache roots.

By default, shows what would be deleted (dry-run). Use --confirm to actually delete.

Safety:
  - Never prunes read-only cache roots
  - Never prunes the version currently pinned in .stagefreight.yml
  - Keeps at least --keep-latest versions per tool`,
	RunE: runToolchainPrune,
}

func init() {
	toolchainPruneCmd.Flags().IntVar(&pruneOlderThan, "older-than", 0, "only prune versions installed more than N days ago")
	toolchainPruneCmd.Flags().StringVar(&pruneTool, "tool", "", "filter to specific tool")
	toolchainPruneCmd.Flags().IntVar(&pruneKeepN, "keep-latest", 1, "keep the N most recent versions per tool")
	toolchainPruneCmd.Flags().BoolVar(&pruneConfirm, "confirm", false, "actually delete (default is dry-run)")

	toolchainCmd.AddCommand(toolchainPruneCmd)
}

type pruneCandidate struct {
	Tool        string
	Version     string
	Dir         string
	InstalledAt time.Time
	Reason      string // why it's being pruned
}

func runToolchainPrune(_ *cobra.Command, _ []string) error {
	rootDir, _ := os.Getwd()
	installRoot := toolchain.InstallRoot(rootDir)

	// Build protected set from config pins
	protected := make(map[string]string) // tool → pinned version
	if cfg != nil {
		for tool, pin := range cfg.Toolchains.Desired {
			if pin.Version != "" {
				protected[tool] = pin.Version
			}
		}
	}

	// Collect all installed versions from writable root
	type versionEntry struct {
		Tool        string
		Version     string
		Dir         string
		InstalledAt time.Time
	}

	byTool := make(map[string][]versionEntry)

	entries, err := os.ReadDir(installRoot)
	if err != nil {
		fmt.Printf("No toolchain cache at %s\n", installRoot)
		return nil
	}

	for _, toolDir := range entries {
		if !toolDir.IsDir() {
			continue
		}
		toolName := toolDir.Name()
		if pruneTool != "" && toolName != pruneTool {
			continue
		}

		versions, err := os.ReadDir(filepath.Join(installRoot, toolName))
		if err != nil {
			continue
		}
		for _, verDir := range versions {
			if !verDir.IsDir() || verDir.Name() == ".lock" {
				continue
			}
			metaPath := filepath.Join(installRoot, toolName, verDir.Name(), ".metadata.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta toolchain.Metadata
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}

			installedAt, _ := time.Parse(time.RFC3339, meta.InstalledAt)
			byTool[toolName] = append(byTool[toolName], versionEntry{
				Tool:        toolName,
				Version:     verDir.Name(),
				Dir:         filepath.Join(installRoot, toolName, verDir.Name()),
				InstalledAt: installedAt,
			})
		}
	}

	// Apply retention rules per tool
	var candidates []pruneCandidate
	now := time.Now()

	for tool, versions := range byTool {
		// Sort newest first
		sort.Slice(versions, func(i, j int) bool {
			return versions[i].InstalledAt.After(versions[j].InstalledAt)
		})

		pinnedVer := protected[tool]

		for i, v := range versions {
			// Never prune pinned version
			if v.Version == pinnedVer {
				continue
			}

			// Keep latest N
			if i < pruneKeepN {
				continue
			}

			// Check age filter
			if pruneOlderThan > 0 {
				age := now.Sub(v.InstalledAt)
				if age < time.Duration(pruneOlderThan)*24*time.Hour {
					continue
				}
			}

			reason := "older version"
			if pruneOlderThan > 0 {
				reason = fmt.Sprintf("older than %d days", pruneOlderThan)
			}

			candidates = append(candidates, pruneCandidate{
				Tool:        v.Tool,
				Version:     v.Version,
				Dir:         v.Dir,
				InstalledAt: v.InstalledAt,
				Reason:      reason,
			})
		}
	}

	if len(candidates) == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}

	// Sort candidates for display
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Tool != candidates[j].Tool {
			return candidates[i].Tool < candidates[j].Tool
		}
		return candidates[i].Version < candidates[j].Version
	})

	if !pruneConfirm {
		fmt.Println("Dry run — would delete:")
		for _, c := range candidates {
			fmt.Printf("  %-14s %-14s %s (%s)\n", c.Tool, c.Version, c.InstalledAt.Format("2006-01-02"), c.Reason)
		}
		fmt.Printf("\n%d versions would be removed. Run with --confirm to delete.\n", len(candidates))
		return nil
	}

	// Actually delete
	deleted := 0
	for _, c := range candidates {
		if err := os.RemoveAll(c.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to delete %s: %v\n", c.Dir, err)
			continue
		}
		fmt.Printf("  deleted %-14s %s\n", c.Tool, c.Version)
		deleted++
	}
	fmt.Printf("\n%d versions removed.\n", deleted)

	return nil
}
