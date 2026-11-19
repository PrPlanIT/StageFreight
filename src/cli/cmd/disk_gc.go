package cmd

import (
	"context"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/disk"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/prune"
)

// runDiskGC is the disk-health pass at the front of each phase that can fill a runner, run
// BEFORE the executor preflight: the executor is what marks the substrate unhealthy on a full disk, so
// reclaiming first means it assesses the TRUE post-GC state instead of failing the run
// on a problem this lifecycle exists to prevent. Pressure-gated and SILENT when the
// disk is healthy — the Executor panel right after already reports free space, so a
// box that says "nothing to do" is noise. A disk still over target is always reported.
// Best-effort: never fails or gates a phase.
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
	pressured := used >= prune.DefaultTarget
	if len(actions) == 0 && !pressured {
		return // healthy — say nothing; the Executor panel carries the disk facts
	}

	freeBefore := prune.FreeBytes(root)
	results := prune.Execute(ctx, actions, true)
	if !pressured && quiet(results) {
		return // only the routine actions ran, and they found nothing
	}
	sec := output.NewSection(os.Stdout, "Disk GC", time.Since(start), color)
	if pressured {
		sec.Row("disk         %.0f%% used ≥ target %.0f%% — enforcing SF-owned lifecycle", used*100, prune.DefaultTarget*100)
	} else {
		sec.Row("disk         %.0f%% used < target %.0f%% — routine lifecycle only", used*100, prune.DefaultTarget*100)
	}
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
		if r.Note != "" {
			sec.Row("  %s", r.Note)
		}
		if r.Err != nil {
			sec.Row("  ⚠ %v", r.Err) // disclosed, never fatal
		}
	}
	sec.Separator()
	// Report the filesystem delta, not the sum of per-action byte counts: the
	// daemon-side prunes (images, builder cache) return no size, so summing what the
	// dir-evictions measured under-reports the real reclaim by orders of magnitude —
	// 83 MiB claimed for a run that moved the disk from 96% to 86%.
	reclaimed := prune.FreeBytes(root) - freeBefore
	if reclaimed < 0 {
		reclaimed = 0 // concurrent writes outpaced the reclaim; do not report a negative
	}
	sec.Row("freed %s · disk %.0f%% → %.0f%% used", humanGCBytes(reclaimed), used*100, prune.UsedFraction(root)*100)
	reportUnreclaimed(ctx, sec, root, prune.DefaultTarget, color)
	sec.Close()
}

// reportUnreclaimed warns when the disk is still at or over target after GC and names the
// biggest holdings.
func reportUnreclaimed(ctx context.Context, sec *output.Section, root string, target float64, color bool) {
	after := prune.UsedFraction(root)
	if target <= 0 || after < target {
		return
	}
	sec.Separator()
	sec.Row("%s disk still %.0f%% used ≥ target %.0f%% — StageFreight has nothing left it may reclaim", output.StatusIcon("warning", color), after*100, target*100)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rep := disk.Scan(sctx, root, nil, 0)
	for _, h := range rep.Largest(5) {
		hint := ""
		if h.Node.Hint != nil {
			hint = " — " + h.Node.Hint.Command
		}
		sec.Row("  %-44s %10s%s", h.Where+" › "+h.Node.Label, humanGCBytes(h.Node.Bytes), hint)
	}
	sec.Row("  the full attribution: stagefreight du (on the runner host, --cache <its cache mount>)")
}

// quiet reports whether no action removed, skipped or failed anything.
func quiet(results []prune.ActionResult) bool {
	for _, r := range results {
		if len(r.Items) > 0 || r.Skipped != "" || r.Err != nil {
			return false
		}
	}
	return true
}
