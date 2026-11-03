package cmd

import (
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
