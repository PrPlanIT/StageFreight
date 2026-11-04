package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/workflow"
)

// workflowRole is --role on commit, push and tag: the role to act as, at most your access
// on the repo allows.
var workflowRole string

// localWorkflow reports whether the local workflow gate applies: a workflow is declared and
// this is not CI. In CI the forge and the CI gate enforce the workflow — StageFreight's own
// automated commits land there under the forge's bypass — so the local gate, which is for
// whoever is at the keyboard, stands aside.
func localWorkflow() bool {
	if cfg == nil || !cfg.Workflow.Present() {
		return false
	}
	return !(&ci.CIContext{Provider: os.Getenv("SF_CI_PROVIDER")}).IsCI()
}

// workflowStanding resolves the role you act as: --role (talent by default), bounded by the
// role your access on the primary repo grants. When that access can't be read the role is
// unbounded and you're told so; the forge still enforces for real on push.
func workflowStanding(out io.Writer) (workflow.Standing, error) {
	ceiling, known, why := forgeCeiling()
	if !known {
		fmt.Fprintf(out, "  workflow: couldn't read your access on the forge (%s); --role is unbounded here, and the forge still enforces on push\n", why)
	}
	return workflow.Effective(ceiling, known, workflowRole)
}

func forgeCeiling() (workflow.Role, bool, string) {
	repo, err := config.ResolvePrimary(cfg.Repos, cfg.Forges, cfg.Vars)
	if err != nil {
		return workflow.Audience, false, err.Error()
	}
	if repo == nil {
		return workflow.Audience, false, "no primary repo declared"
	}
	fc, err := forge.NewFromAccessory(repo.Provider, repo.BaseURL, repo.Project, repo.Credentials)
	if err != nil {
		return workflow.Audience, false, err.Error()
	}
	reader, ok := fc.(forge.AccessReader)
	if !ok {
		return workflow.Audience, false, repo.Provider + " can't report access levels"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	level, err := reader.Access(ctx)
	if err != nil {
		return workflow.Audience, false, err.Error()
	}
	return workflow.RoleFor(level), true, ""
}

// workflowRefusals reports findings under enforcement: evaluate, which pass, and returns the
// rest for the caller to refuse or override.
func workflowRefusals(out io.Writer, findings ...*workflow.Finding) []*workflow.Finding {
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

// workflowGate refuses an action that breaks the declared workflow. A producer may override
// it with --maintainer-override, which is reported, so it is never silent.
func workflowGate(out io.Writer, override bool, s workflow.Standing, findings ...*workflow.Finding) error {
	refused := workflowRefusals(out, findings...)
	if len(refused) == 0 {
		return nil
	}
	if override {
		if err := overrideAllowed(s); err != nil {
			return err
		}
		for _, f := range refused {
			fmt.Fprintf(out, "  workflow rule overridden (--maintainer-override, as %s): %s\n", s.Role, f.Message)
		}
		return nil
	}
	var b strings.Builder
	for _, f := range refused {
		fmt.Fprintf(&b, "%s\n  %s\n", f.Message, f.Teach)
	}
	b.WriteString("a producer can proceed anyway with --role producer --maintainer-override (the forge still enforces its own rules)")
	return errors.New(b.String())
}

// overrideAllowed refuses --maintainer-override below the producer role.
func overrideAllowed(s workflow.Standing) error {
	if workflow.CanOverride(s) {
		return nil
	}
	return fmt.Errorf("--maintainer-override needs the producer role; you're acting as %s", s.Role)
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
