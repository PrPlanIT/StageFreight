package docker

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
)

// publishPhase writes the docker publish manifest.
// Separate from the generic PublishManifestPhase because docker builds manage their own
// manifest accumulation (multi-platform digests, signing records, etc.).
func publishPhase() pipeline.Phase {
	return pipeline.Phase{
		Name: "publish",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			publishModeUsed, _ := pc.Scratch["docker.publishModeUsed"].(bool)
			if !publishModeUsed {
				return &pipeline.PhaseResult{
					Name:    "publish",
					Status:  "skipped",
					Summary: "no artifacts published",
				}, nil
			}

			publishManifest := pc.Scratch["docker.publishManifest"].(*artifact.PublishManifest)
			if err := artifact.WritePublishManifest(pc.RootDir, *publishManifest); err != nil {
				return nil, fmt.Errorf("writing publish manifest: %w", err)
			}

			// Also print image references
			if result, ok := pc.Scratch["docker.buildResult"].(*build.BuildResult); ok {
				fmt.Fprintf(pc.Writer, "\n    Image References\n")
				for _, sr := range result.Steps {
					for _, img := range sr.Images {
						fmt.Fprintf(pc.Writer, "    → %s\n", img)
					}
				}
				fmt.Fprintln(pc.Writer)
			}

			return &pipeline.PhaseResult{
				Name:    "publish",
				Status:  "success",
				Summary: fmt.Sprintf("%d image(s)", len(publishManifest.Published)),
			}, nil
		},
	}
}

// renderPlan renders the dry-run plan output for docker builds.
func renderPlan(pc *pipeline.PipelineContext) {
	plan := pc.BuildPlan
	if plan == nil {
		return
	}
	for _, step := range plan.Steps {
		fmt.Fprintf(pc.Writer, "step: %s\n", step.Name)
		fmt.Fprintf(pc.Writer, "  dockerfile: %s\n", step.Dockerfile)
		fmt.Fprintf(pc.Writer, "  context:    %s\n", step.Context)
		fmt.Fprintf(pc.Writer, "  target:     %s\n", step.Target)
		fmt.Fprintf(pc.Writer, "  platforms:  %v\n", step.Platforms)
		fmt.Fprintf(pc.Writer, "  tags:       %v\n", step.Tags)
		fmt.Fprintf(pc.Writer, "  load:       %v\n", step.Load)
		fmt.Fprintf(pc.Writer, "  push:       %v\n", step.Push)
		if len(step.BuildArgs) > 0 {
			fmt.Fprintf(pc.Writer, "  build_args: %v\n", step.BuildArgs)
		}
	}
}
