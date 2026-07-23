package governance

import (
	"fmt"
	"strings"
)

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
