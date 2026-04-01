package governance

import (
	"fmt"
	"strings"
)

// ExplainTrace returns a detailed per-entry trace for debugging.
func ExplainTrace(trace MergeTrace) string {
	var b strings.Builder

	b.WriteString("Resolution trace:\n")
	for _, e := range trace.Entries {
		line := fmt.Sprintf("  %-40s %-10s source=%-30s layer=%d",
			e.Path, e.Operation, e.Source, e.Layer)
		if e.SourceRef != "" {
			line += fmt.Sprintf(" ref=%s", e.SourceRef)
		}
		if e.Overridden {
			line += fmt.Sprintf(" (overridden by %s)", e.OverriddenBy)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

// RenderGated returns the execution plan as human-readable output (after gating).
func RenderGated(plan ExecutionPlan) string {
	var b strings.Builder

	b.WriteString("Execution plan:\n")

	if len(plan.Enabled) > 0 {
		b.WriteString("\n  Enabled:\n")
		for _, f := range plan.Enabled {
			b.WriteString(fmt.Sprintf("    %-30s %s\n", f.Domain, f.Reason))
		}
	}

	if len(plan.Skipped) > 0 {
		b.WriteString("\n  Skipped:\n")
		for _, f := range plan.Skipped {
			b.WriteString(fmt.Sprintf("    %-30s %s\n", f.Domain, f.Reason))
		}
	}

	return b.String()
}
