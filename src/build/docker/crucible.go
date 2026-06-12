package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
)

// resolveBuildMode determines the active build mode.
// Priority: recursion guard → CLI flag → config file → default "".
func resolveBuildMode(req Request) string {
	if build.IsCrucibleChild() {
		return ""
	}
	if req.BuildMode != "" {
		return req.BuildMode
	}
	if req.Config != nil {
		for _, b := range req.Config.Builds {
			if b.Kind == "docker" && b.BuildMode != "" {
				if req.BuildID == "" || b.ID == req.BuildID {
					return b.BuildMode
				}
			}
		}
	}
	return ""
}

// buildStepWithRecovery builds one step, retrying once on a recoverable push
// failure (vendor recovery) and once more without cache export if export failed.
// Buffers are reset before each attempt; the returned StepResult is never nil.
func buildStepWithRecovery(ctx context.Context, bx *Buildx, step build.BuildStep, stdoutBuf, stderrBuf *bytes.Buffer) (*build.StepResult, error) {
	stdoutBuf.Reset()
	stderrBuf.Reset()
	stepResult, layers, err := bx.BuildWithLayers(ctx, step)
	if stepResult == nil {
		stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
	}
	stepResult.Layers = layers

	if err != nil && step.Push {
		failure := postbuild.PushFailure{Err: err, ExitCode: extractExitCode(err), Stderr: stdoutBuf.String() + "\n" + stderrBuf.String()}
		if rec := postbuild.RecoverPushFailure(ctx, step.Registries, failure); rec.Retry {
			diag.Info(rec.Message)
			stdoutBuf.Reset()
			stderrBuf.Reset()
			stepResult, layers, err = bx.BuildWithLayers(ctx, step)
			if stepResult == nil {
				stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
			}
			stepResult.Layers = layers
		}
	}

	if err != nil && len(step.CacheTo) > 0 && isCacheExportError(err, stdoutBuf.String()+"\n"+stderrBuf.String()) {
		diag.Warn("cache export failed — retrying build without cache export")
		retry := step
		retry.CacheTo = nil
		stdoutBuf.Reset()
		stderrBuf.Reset()
		stepResult, layers, err = bx.BuildWithLayers(ctx, retry)
		if stepResult == nil {
			stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
		}
		stepResult.Layers = layers
	}

	return stepResult, err
}

// executeBuildPass runs a single build pass and renders structured output.
// resultTag: if non-empty, shows "result <tag>". If empty, shows pushed tags from plan steps.
func executeBuildPass(ctx context.Context, w io.Writer, color, verbose bool, stderr io.Writer,
	sectionName string, plan *build.BuildPlan, resultTag string) (*build.BuildResult, error) {

	buildStart := time.Now()

	bx := NewBuildx(verbose)
	var stderrBuf, stdoutBuf bytes.Buffer
	bx.Stdout = &stdoutBuf
	if verbose {
		bx.Stderr = stderr
	} else {
		bx.Stderr = &stderrBuf
	}

	var result build.BuildResult
	for _, step := range plan.Steps {
		stepResult, err := buildStepWithRecovery(ctx, bx, step, &stdoutBuf, &stderrBuf)
		result.Steps = append(result.Steps, *stepResult)
		if err != nil {
			elapsed := time.Since(buildStart)
			failSec := output.NewSection(w, sectionName, elapsed, color)
			renderBuildLayers(failSec, result.Steps, color)
			output.RowStatus(failSec, "status", "build failed", "failed", color)

			combinedOutput := stdoutBuf.String() + "\n" + stderrBuf.String()
			RenderBuildError(failSec, combinedOutput)
			failSec.Close()
			return &result, fmt.Errorf("%s failed: %w", sectionName, err)
		}
	}

	elapsed := time.Since(buildStart)
	sec := output.NewSection(w, sectionName, elapsed, color)
	renderBuildLayers(sec, result.Steps, color)
	if resultTag != "" {
		sec.Separator()
		sec.Row("result  %s", resultTag)
	} else {
		// Disposition rendered by execution shape: a retained step (transport:
		// Push=false + OCILayoutDir) prints only a deferral note — it contacted no
		// registry, so it must not appear pushed. Only a Push=true step lists tags.
		sec.Separator()
		for _, step := range plan.Steps {
			if !step.Push && step.OCILayoutDir != "" {
				sec.Row("%s  retained — distribution deferred to publish phase",
					output.StatusIcon("skipped", color))
				continue
			}
			for _, tag := range step.Tags {
				sec.Row("%s  %s  (pushed)", output.StatusIcon("success", color), tag)
			}
		}
	}
	sec.Close()

	return &result, nil
}

// clonePlan deep copies a build plan so mutations don't affect the original.
func clonePlan(plan *build.BuildPlan) *build.BuildPlan {
	clone := *plan
	clone.Steps = make([]build.BuildStep, len(plan.Steps))
	for i, s := range plan.Steps {
		clone.Steps[i] = s
		clone.Steps[i].Tags = append([]string{}, s.Tags...)
		if s.CacheFrom != nil {
			clone.Steps[i].CacheFrom = append([]build.CacheRef{}, s.CacheFrom...)
		}
		if s.CacheTo != nil {
			clone.Steps[i].CacheTo = append([]build.CacheRef{}, s.CacheTo...)
		}
	}
	return &clone
}

func crucibleVerdict(w io.Writer, title, body string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    ──────────────────────────────────────────────────────────────")
	fmt.Fprintf(w, "    🐘 Crucible Verdict: %s\n", title)
	fmt.Fprintf(w, "    %s\n", body)
	fmt.Fprintln(w, "    ──────────────────────────────────────────────────────────────")
	fmt.Fprintln(w)
}

func checkStatusIcon(status string, color bool) string {
	switch status {
	case "match":
		return output.StatusIcon("success", color)
	case "differs":
		return output.StatusIcon("failed", color)
	case "info":
		return output.StatusIcon("warning", color)
	default:
		return output.StatusIcon("skipped", color)
	}
}
