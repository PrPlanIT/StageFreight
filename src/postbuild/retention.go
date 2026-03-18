package postbuild

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// HasRetention returns true if any step has a registry with retention configured.
func HasRetention(plan *build.BuildPlan) bool {
	for _, step := range plan.Steps {
		if len(step.Registries) == 0 {
			continue
		}
		for _, reg := range step.Registries {
			if reg.Retention.Active() {
				return true
			}
		}
	}
	return false
}

// RetentionHook applies tag retention to configured registries.
func RetentionHook() pipeline.PostBuildHook {
	return pipeline.PostBuildHook{
		Name: "retention",
		Condition: func(pc *pipeline.PipelineContext) bool {
			return pc.BuildPlan != nil && HasRetention(pc.BuildPlan)
		},
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			summary, _ := RunRetentionSection(pc.Ctx, pc.Writer, pc.CI, pc.Color, pc.BuildPlan)
			return &pipeline.PhaseResult{
				Name:    "retention",
				Status:  "success",
				Summary: summary,
			}, nil
		},
	}
}

// RunRetentionSection applies tag retention with section-formatted output.
// Returns a summary string and elapsed time for the summary table.
func RunRetentionSection(ctx context.Context, w io.Writer, _ bool, color bool, plan *build.BuildPlan) (string, time.Duration) {
	output.SectionStartCollapsed(w, "sf_retention", "Retention")
	retStart := time.Now()

	var totalDeleted int
	var totalKept int
	var totalSkipped int
	var totalErrors int
	var deletedNames []string
	var skippedNames []string

	for _, step := range plan.Steps {
		if len(step.Registries) == 0 {
			continue
		}
		for _, reg := range step.Registries {
			if !reg.Retention.Active() {
				continue
			}

			client, err := registry.NewRegistry(reg.Provider, reg.URL, reg.Credentials)
			if err != nil {
				fmt.Fprintf(w, "  ERROR: %s/%s: %v\n", reg.URL, reg.Path, err)
				totalErrors++
				continue
			}

			// Copy policy and protect produced tags from deletion.
			policy := reg.Retention
			policy.Protect = append([]string{}, policy.Protect...)
			for _, t := range reg.Tags {
				policy.Protect = append(policy.Protect, t)
			}

			result, err := registry.ApplyRetention(ctx, client, reg.Path, reg.TagPatterns, policy)
			if err != nil {
				fmt.Fprintf(w, "  ERROR: %s/%s: %v\n", reg.URL, reg.Path, err)
				totalErrors++
				continue
			}

			for _, e := range result.Errors {
				fmt.Fprintf(w, "  ERROR: %v\n", e)
			}

			totalKept += result.Kept
			totalDeleted += len(result.Deleted)
			totalSkipped += len(result.Skipped)
			totalErrors += len(result.Errors)
			deletedNames = append(deletedNames, result.Deleted...)
			skippedNames = append(skippedNames, result.Skipped...)
		}
	}

	retElapsed := time.Since(retStart)

	sec := output.NewSection(w, "Retention", retElapsed, color)
	for _, step := range plan.Steps {
		for _, reg := range step.Registries {
			if !reg.Retention.Active() {
				continue
			}
			sec.Row("%-40skept %d, pruned %d", reg.URL+"/"+reg.Path, totalKept, totalDeleted)
		}
	}
	for _, d := range deletedNames {
		sec.Row("  - %s", d)
	}
	for _, s := range skippedNames {
		sec.Row("  ~ %s (digest shared with protected tag)", s)
	}
	sec.Close()
	output.SectionEnd(w, "sf_retention")

	summary := fmt.Sprintf("kept %d, pruned %d", totalKept, totalDeleted)
	if totalSkipped > 0 {
		summary += fmt.Sprintf(", %d skipped", totalSkipped)
	}
	if totalErrors > 0 {
		summary += fmt.Sprintf(", %d error(s)", totalErrors)
	}

	return summary, retElapsed
}
