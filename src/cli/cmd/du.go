package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/disk"
)

var (
	duJSON      bool
	duCacheRoot string
	duRepos     string
	duNoRepos   bool
	duMaxDepth  int
)

var duCmd = &cobra.Command{
	Use:   "du",
	Short: "Storage-attribution diagnostic — what is eating disk today",
	Long: "Report what StageFreight and its CI occupy on disk, grouped so an operator can act: " +
		"the persistent cache mount (toolchains by version, build/scan caches by subsystem, " +
		"per-project rust targets), the Docker daemon(s) (host vs dind, images by family with " +
		"tags, dangling, volumes, build cache), and discovered repositories. Bars are share of " +
		"total disk; a reclaim ledger names the biggest wins. Read-only.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		var repoRoots []string
		if !duNoRepos {
			if duRepos != "" {
				repoRoots = splitClean(duRepos)
			} else if home, err := os.UserHomeDir(); err == nil {
				repoRoots = []string{home}
			}
		}

		rep := disk.Scan(ctx, duCacheRoot, repoRoots, duMaxDepth)
		if duJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		host, _ := os.Hostname()
		if host == "" {
			host = "host"
		}
		disk.Render(os.Stdout, host, rep, disk.RenderOpts{})
		return nil
	},
}

func init() {
	duCmd.Flags().BoolVar(&duJSON, "json", false, "machine-readable JSON output")
	duCmd.Flags().StringVar(&duCacheRoot, "cache", "", "persistent cache mount path (default /stagefreight; on a runner host use e.g. /opt/docker/gitlab-runner/stagefreight)")
	duCmd.Flags().StringVar(&duRepos, "repos", "", "comma-separated roots to discover repositories under (default: $HOME)")
	duCmd.Flags().BoolVar(&duNoRepos, "no-repos", false, "skip repository discovery")
	duCmd.Flags().IntVar(&duMaxDepth, "max-depth", 3, "repository discovery recursion depth")
	rootCmd.AddCommand(duCmd)
}

func splitClean(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
