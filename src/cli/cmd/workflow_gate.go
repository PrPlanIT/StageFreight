package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/workflow"
)

// workflowRefusals holds an action to the declared workflow at the local boundary. Findings
// under enforcement: evaluate are reported and pass; the rest are returned for the caller to
// refuse or override. In CI the forge and the CI gate enforce the workflow — StageFreight's
// own automated commits land there under the forge's bypass — so the local gate, which is
// for whoever is at the keyboard, stands aside.
func workflowRefusals(out io.Writer, findings ...*workflow.Finding) []*workflow.Finding {
	if (&ci.CIContext{Provider: os.Getenv("SF_CI_PROVIDER")}).IsCI() {
		return nil
	}
	var refused []*workflow.Finding
	for _, f := range findings {
		switch {
		case f == nil:
		case f.Evaluate:
			fmt.Fprintf(out, "  workflow (evaluate, not enforced): %s\n", f.Message)
		default:
			refused = append(refused, f)
		}
	}
	return refused
}

// workflowGate refuses an action that breaks the declared workflow unless overridden; an
// override is reported, so it is never silent.
func workflowGate(out io.Writer, override bool, findings ...*workflow.Finding) error {
	refused := workflowRefusals(out, findings...)
	if len(refused) == 0 {
		return nil
	}
	if override {
		for _, f := range refused {
			fmt.Fprintf(out, "  workflow rule overridden (--maintainer-override): %s\n", f.Message)
		}
		return nil
	}
	var b strings.Builder
	for _, f := range refused {
		fmt.Fprintf(&b, "%s\n  %s\n", f.Message, f.Teach)
	}
	b.WriteString("re-run with --maintainer-override to proceed anyway (the forge still enforces its own rules)")
	return errors.New(b.String())
}

// workflowBlocks turns refused findings into commit governance blocks, which
// --maintainer-override bypasses and the commit result records.
func workflowBlocks(out io.Writer, findings ...*workflow.Finding) commit.CommitBlocks {
	var blocks commit.CommitBlocks
	for _, f := range workflowRefusals(out, findings...) {
		blocks = append(blocks, commit.CommitBlock{
			Ring:    commit.RingGovernance,
			ID:      "workflow." + f.Rule,
			Message: f.Message + "; " + f.Teach,
		})
	}
	return blocks
}
