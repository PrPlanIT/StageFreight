package pipeline

import (
	"time"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// renderSummary writes the summary table from accumulated PhaseResults.
func renderSummary(pc *PipelineContext) {
	if len(pc.Results) == 0 {
		return
	}

	totalElapsed := time.Since(pc.PipelineStart)
	overallStatus := "success"

	sumSec := output.NewSection(pc.Writer, "Summary", 0, pc.Color)

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
		}

		if r.Summary != "" {
			output.SummaryRow(pc.Writer, r.Name, r.Status, r.Summary, pc.Color)
		}
	}

	sumSec.Separator()
	output.SummaryTotal(pc.Writer, totalElapsed, overallStatus, pc.Color)
	sumSec.Close()
}
