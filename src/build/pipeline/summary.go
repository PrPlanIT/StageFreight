package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// FailureDetailFile is the well-known path (relative to rootDir) where the
// inner build writes FailureDetail for the outer crucible to pick up.
const FailureDetailFile = ".stagefreight/failure-detail.json"

// renderSummary writes the summary table from accumulated PhaseResults.
func renderSummary(pc *PipelineContext) {
	if len(pc.Results) == 0 {
		return
	}

	totalElapsed := time.Since(pc.PipelineStart)
	overallStatus := "success"

	sumSec := output.NewSection(pc.Writer, "Summary", 0, pc.Color)

	var failure *FailureDetail
	for _, r := range pc.Results {
		// Skip banner from summary — it's infrastructure, not a reportable phase
		if r.Name == "banner" {
			continue
		}
		// Skip dry-run gate when it didn't activate
		if r.Name == "dry-run" && r.Status == "skipped" {
			continue
		}

		if r.Status == "failed" {
			overallStatus = "failed"
			if r.Failure != nil && failure == nil {
				failure = r.Failure
			}
		}

		if r.Summary != "" {
			output.SummaryRow(pc.Writer, r.Name, r.Status, r.Summary, pc.Color)
		}
	}

	sumSec.Separator()
	output.SummaryTotal(pc.Writer, totalElapsed, overallStatus, pc.Color)
	sumSec.Close()

	if failure == nil {
		return
	}

	// Crucible child: write FailureDetail to disk for the outer process.
	// Do NOT render Exit Reason — the outer crucible owns that.
	if build.IsCrucibleChild() {
		writeFailureDetail(pc.RootDir, failure)
		return
	}

	// Standard pipeline: render Exit Reason inline after the summary.
	RenderExitReason(pc.Writer, failure)
}

// writeFailureDetail persists a FailureDetail as JSON for the outer crucible.
func writeFailureDetail(rootDir string, f *FailureDetail) {
	p := filepath.Join(rootDir, FailureDetailFile)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o644)
}

// ReadFailureDetail loads a FailureDetail written by a crucible child.
// Returns nil if the file doesn't exist or can't be parsed.
func ReadFailureDetail(rootDir string) *FailureDetail {
	p := filepath.Join(rootDir, FailureDetailFile)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f FailureDetail
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return &f
}

// RenderExitReason renders the operator-facing Exit Reason box.
// Single-line when command + exit code + reason fit in ~80 chars, two-line otherwise.
// Exported so the outer crucible path can call it.
func RenderExitReason(w io.Writer, f *FailureDetail) {
	exitSuffix := ""
	if f.ExitCode != 0 {
		exitSuffix = fmt.Sprintf(" (exit %d)", f.ExitCode)
	}

	oneLine := fmt.Sprintf("%s%s — %s", f.Command, exitSuffix, f.Reason)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "    ── Exit Reason ────────────────────────────────────────────")
	if len(oneLine) <= 80 {
		fmt.Fprintf(w, "    │ %s\n", oneLine)
	} else {
		fmt.Fprintf(w, "    │ %s%s\n", f.Command, exitSuffix)
		fmt.Fprintf(w, "    │ reason: %s\n", f.Reason)
	}
	fmt.Fprintln(w, "    └──────────────────────────────────────────────────────────────")
}
