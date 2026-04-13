package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

var toolchainListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show installed toolchain versions",
	RunE:  runToolchainList,
}

func init() {
	toolchainCmd.AddCommand(toolchainListCmd)
}

type installedTool struct {
	Tool        string
	Version     string
	CacheRoot   string
	CacheLabel  string // "persistent" or "workspace"
	InstalledAt string
	SourceURL   string
}

func runToolchainList(_ *cobra.Command, _ []string) error {
	rootDir, _ := os.Getwd()
	roots := toolchain.ReadRoots(rootDir)

	var installed []installedTool

	for _, root := range roots {
		label := "workspace"
		if root == toolchain.PersistentCacheRoot() {
			label = "persistent"
		}

		// Walk <root>/<tool>/<version>/.metadata.json
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, toolDir := range entries {
			if !toolDir.IsDir() {
				continue
			}
			toolName := toolDir.Name()
			versions, err := os.ReadDir(filepath.Join(root, toolName))
			if err != nil {
				continue
			}
			for _, verDir := range versions {
				if !verDir.IsDir() {
					continue
				}
				metaPath := filepath.Join(root, toolName, verDir.Name(), ".metadata.json")
				data, err := os.ReadFile(metaPath)
				if err != nil {
					continue
				}
				var meta toolchain.Metadata
				if err := json.Unmarshal(data, &meta); err != nil {
					continue
				}
				installed = append(installed, installedTool{
					Tool:        toolName,
					Version:     verDir.Name(),
					CacheRoot:   root,
					CacheLabel:  label,
					InstalledAt: meta.InstalledAt,
					SourceURL:   meta.SourceURL,
				})
			}
		}
	}

	sort.Slice(installed, func(i, j int) bool {
		if installed[i].Tool != installed[j].Tool {
			return installed[i].Tool < installed[j].Tool
		}
		return installed[i].Version < installed[j].Version
	})

	if len(installed) == 0 {
		fmt.Println("No toolchains installed.")
		return nil
	}

	// Render table
	fmt.Printf("%-14s %-14s %-12s %-22s %s\n", "TOOL", "VERSION", "CACHE", "INSTALLED", "SOURCE")
	for _, t := range installed {
		ts := t.InstalledAt
		if len(ts) > 19 {
			ts = ts[:19]
		}
		src := t.SourceURL
		if len(src) > 60 {
			src = src[:57] + "..."
		}
		fmt.Printf("%-14s %-14s %-12s %-22s %s\n", t.Tool, t.Version, t.CacheLabel, ts, src)
	}

	// Show pinned versions from config if available
	if cfg != nil && len(cfg.Toolchains.Desired) > 0 {
		fmt.Println()
		fmt.Println("Pinned (from .stagefreight.yml):")
		var keys []string
		for k := range cfg.Toolchains.Desired {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-14s %s\n", k, cfg.Toolchains.Desired[k].Version)
		}
	}

	return nil
}

// truncate is a helper for display strings.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// pad right with spaces.
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
