package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/prune"
)

var (
	pruneCacheRoot   string
	pruneTarget      float64
	pruneDoConfirm   bool
	pruneHostCleanup bool
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Reclaim disk from StageFreight-owned artifacts (dry-run by default)",
	Long: `Apply the SF disk lifecycle: rotate what StageFreight itself created — the cache
root's subsystems (named policies + a backstop for the rest), toolchain versions
beyond toolchains.retention, the repo's own published image generations, and the
sf-builder's cache — under the declared retention grammar.

Ownership is the invariant: StageFreight never guesses whether something belongs to
someone else. Third-party artifacts are reclaimed ONLY via the operator's declared
adoptions (build_cache.cleanup.prune.images.refs), and only under --host-cleanup.

Dry-run by default: shows the plan with each item's ownership class. --confirm executes.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root := pruneCacheRoot
		if root == "" {
			root = paths.ResolveCacheRoot("")
		}
		hostCleanup := pruneHostCleanup
		if cfg != nil && cfg.BuildCache.Cleanup.Enabled {
			hostCleanup = true // declared authorization in config
		}
		actions := prune.Plan(cfg, prune.Options{
			CacheRoot: root, Target: pruneTarget, HostCleanup: hostCleanup,
		})

		color := output.UseColor()
		start := time.Now()
		used := prune.UsedFraction(root)
		if len(actions) == 0 {
			sec := output.NewSection(os.Stdout, "Disk GC", time.Since(start), color)
			sec.Row("disk         %.0f%% used — healthy, nothing to do", used*100)
			sec.Close()
			return nil
		}

		results := prune.Execute(cmd.Context(), actions, pruneDoConfirm)
		mode := "dry-run"
		if pruneDoConfirm {
			mode = "apply"
		}
		sec := output.NewSection(os.Stdout, "Disk GC", time.Since(start), color)
		sec.Row("mode         %s · disk %.0f%% used · cache %s", mode, used*100, root)
		sec.Separator()
		var freed int64
		var failed int
		for _, r := range results {
			switch {
			case r.Skipped != "":
				sec.Row("⊘ %-28s %s", r.Action.Label, r.Skipped)
			case len(r.Items) == 0:
				sec.Row("· %-28s within policy [%s]", r.Action.Label, r.Action.Class)
			default:
				sec.Row("♻ %-28s %d item(s) [%s] — %s", r.Action.Label, len(r.Items), r.Action.Class, r.Action.Reason)
				for i, it := range r.Items {
					if i == 6 {
						sec.Row("    … +%d more", len(r.Items)-i)
						break
					}
					sec.Row("    - %s", it)
				}
			}
			freed += r.Freed
			if r.Err != nil {
				failed++
				sec.Row("  ⚠ %v", r.Err)
			}
		}
		sec.Separator()
		verb := "would free"
		if pruneDoConfirm {
			verb = "freed"
		}
		sec.Row("%s ≥ %s (dir-evictions measured; daemon prunes report separately)", verb, humanGCBytes(freed))
		if !pruneDoConfirm {
			sec.Row("dry-run — re-run with --confirm to execute")
		}
		sec.Close()
		if failed > 0 {
			return fmt.Errorf("disk gc: %d action(s) reported errors", failed)
		}
		return nil
	},
}

func init() {
	pruneCmd.Flags().StringVar(&pruneCacheRoot, "cache", "", "SF cache mount root (default: resolved like `du`)")
	pruneCmd.Flags().Float64Var(&pruneTarget, "target", 0, "only act when disk used-fraction ≥ target (0 = always enforce policy)")
	pruneCmd.Flags().BoolVar(&pruneDoConfirm, "confirm", false, "execute the plan (default: dry-run)")
	pruneCmd.Flags().BoolVar(&pruneHostCleanup, "host-cleanup", false, "authorize the declared host-cleanup policies for this invocation (build_cache.cleanup)")
	rootCmd.AddCommand(pruneCmd)
}

func humanGCBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d B", b)
}
