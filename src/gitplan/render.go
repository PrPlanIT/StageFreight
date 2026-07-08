package gitplan

import (
	"fmt"
	"strings"
)

// Render turns a Plan into the block shown to the operator before anything runs — the
// "plan IS the UX". Deterministic (golden-testable). Render is one consumer of the graph;
// Execute walks the SAME graph, so the two can never describe different things.
func Render(p Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %s  [%s]\n", p.Summary, p.Interaction())
	fmt.Fprintf(&b, "  destination: %s\n", p.Dest.Ref())
	for _, op := range p.Operations {
		line := "  " + string(op.Kind)
		if op.Detail != "" {
			line += " — " + op.Detail
		}
		b.WriteString(line + "\n")
		if len(op.Choices) > 0 {
			fmt.Fprintf(&b, "      choices: %s\n", strings.Join(op.Choices, " / "))
		}
	}
	return b.String()
}
