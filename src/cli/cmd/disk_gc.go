package cmd

import (
	"context"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/prune"
)

// runDiskGC is the audition-phase disk-health pass, run BEFORE the executor
// preflight: the executor is what marks the substrate unhealthy on a full disk, so
// reclaiming first means it assesses the TRUE post-GC state instead of failing the run
// on a problem this lifecycle exists to prevent. Pressure-gated and SILENT when the
// disk is healthy — the Executor panel right after already reports free space, so a
// box that says "nothing to do" is noise. Best-effort: never fails or gates a phase.
func runDiskGC(ctx context.Context, appCfg *config.Config) {
	root := paths.ResolveCacheRoot("")
	if root == "" {
		return // no writable cache root — nothing SF-owned to manage here
	}
	start := time.Now()
	color := output.UseColor()
	used := prune.UsedFraction(root)

	hostCleanup := appCfg != nil && appCfg.BuildCache.Cleanup.Enabled
	actions := prune.Plan(appCfg, prune.Options{
		CacheRoot: root, Target: prune.DefaultTarget, HostCleanup: hostCleanup,
	})
	if len(actions) == 0 {
		return // healthy — say nothing; the Executor panel carries the disk facts
	}

	results := prune.Execute(ctx, actions, true)
	sec := output.NewSection(os.Stdout, "Disk GC", time.Since(start), color)
	sec.Row("disk         %.0f%% used ≥ target %.0f%% — enforcing SF-owned lifecycle", used*100, prune.DefaultTarget*100)
	sec.Separator()
	var freed int64
	for _, r := range results {
		switch {
		case r.Skipped != "":
			sec.Row("⊘ %-28s %s", r.Action.Label, r.Skipped)
		case len(r.Items) == 0:
			sec.Row("· %-28s within policy", r.Action.Label)
		default:
			sec.Row("♻ %-28s %d item(s) [%s]", r.Action.Label, len(r.Items), r.Action.Class)
		}
		freed += r.Freed
		if r.Err != nil {
			sec.Row("  ⚠ %v", r.Err) // disclosed, never fatal
		}
	}
	sec.Separator()
	sec.Row("freed ≥ %s · now %.0f%% used", humanGCBytes(freed), prune.UsedFraction(root)*100)
	sec.Close()
}
