package cmd

import (
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/output"
)

// upstreamSection opens the "Upstream" output box — the single, consistent frame for every
// upstream action (watch, bump, sync, release). It replaces the scattered `upstream: …`
// prints so the one phase that mutates reads as legibly as every other phase: a boxed
// section with verdict rows, not loose prose a reader has to reassemble.
func upstreamSection() *output.Section {
	return output.NewSection(os.Stdout, "Upstream", 0, output.UseColor())
}

// upstreamRows collects one upstream step's (label, text) rows so the step draws a single
// Upstream box once it is done. A nil collector drops its rows.
type upstreamRows [][2]string

func (r *upstreamRows) add(label, format string, args ...any) {
	if r == nil {
		return
	}
	*r = append(*r, [2]string{label, fmt.Sprintf(format, args...)})
}

// render draws the collected rows as one Upstream section; nothing is drawn when empty.
func (r upstreamRows) render() {
	if len(r) == 0 {
		return
	}
	sec := upstreamSection()
	for _, row := range r {
		sec.Row("%-14s%s", row[0], row[1])
	}
	sec.Close()
}
