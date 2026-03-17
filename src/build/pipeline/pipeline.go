package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// ErrDryRunExit is a sentinel returned by DryRunGate to signal a clean exit.
// Pipeline.Run recognizes this and returns nil after rendering a partial summary.
var ErrDryRunExit = errors.New("dry-run exit")

// PipelineContext is the shared state bag threaded through all phases.
type PipelineContext struct {
	Ctx           context.Context
	RootDir       string
	Config        *config.Config
	Writer        io.Writer
	Color         bool
	CI            bool
	Verbose       bool
	SkipLint      bool
	DryRun        bool
	Local         bool
	PipelineStart time.Time
	Manifest      build.PublishManifest // accumulated by execute phases
	Results       []PhaseResult        // accumulated by pipeline runner

	// Scratch is a typed state bag for command-specific data flowing between phases.
	// Keys: "binary.steps", "docker.plan", "detect.result", etc.
	Scratch map[string]any
}

// Pipeline orchestrates build phases and hooks.
type Pipeline struct {
	Phases []Phase
	Hooks  []PostBuildHook
}

// Run iterates phases in order.
// On phase error (and StopOnPhaseError): renders partial summary, returns error.
// On ErrDryRunExit: renders partial summary, returns nil (clean exit).
// Then runs hooks conditionally (nil Condition = always run; errors recorded, not fatal).
// Finally renders summary table from pc.Results.
func (p *Pipeline) Run(pc *PipelineContext) error {
	if pc.Writer == nil {
		pc.Writer = os.Stdout
	}
	if pc.Scratch == nil {
		pc.Scratch = make(map[string]any)
	}

	var phaseErr error
	for _, phase := range p.Phases {
		start := time.Now()
		result, err := phase.Run(pc)
		elapsed := time.Since(start)

		if result != nil {
			if result.Elapsed == 0 {
				result.Elapsed = elapsed
			}
			pc.Results = append(pc.Results, *result)
		} else if err != nil && !errors.Is(err, ErrDryRunExit) {
			// Phase failed without returning a result — synthesize one
			pc.Results = append(pc.Results, PhaseResult{
				Name:    phase.Name,
				Status:  "failed",
				Summary: err.Error(),
				Elapsed: elapsed,
			})
		}

		if err != nil {
			if errors.Is(err, ErrDryRunExit) {
				renderSummary(pc)
				return nil
			}
			phaseErr = err
			break
		}
	}

	if phaseErr != nil {
		renderSummary(pc)
		return phaseErr
	}

	// Run hooks — errors are recorded but not fatal
	for _, hook := range p.Hooks {
		if hook.Condition != nil && !hook.Condition(pc) {
			continue
		}
		start := time.Now()
		result, err := hook.Run(pc)
		elapsed := time.Since(start)

		if result != nil {
			if result.Elapsed == 0 {
				result.Elapsed = elapsed
			}
			pc.Results = append(pc.Results, *result)
		} else if err != nil {
			pc.Results = append(pc.Results, PhaseResult{
				Name:    hook.Name,
				Status:  "failed",
				Summary: fmt.Sprintf("hook error: %v", err),
				Elapsed: elapsed,
			})
		}
		// Hook errors are non-fatal — continue to next hook
	}

	renderSummary(pc)
	return nil
}
