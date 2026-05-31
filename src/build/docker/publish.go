package docker

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
)

// writeManifests is the single owner of the v2 emission sequence: write
// outputs.json (intent), build the results manifest — which enforces the
// outputs↔results ArtifactID join and binds the intent checksum — then write
// published.json (observations). Returns the built ResultsManifest so callers
// can report on recorded outcomes.
//
// Both execution paths route through here: the docker pipeline's publishPhase
// and crucible mode's verified-publish pass. Keeping the write/build/write
// trio in one place means the two paths cannot drift in emission order or
// error semantics.
func writeManifests(rootDir string, outputs *artifact.OutputsManifest, rb *build.ResultsBuilder) (artifact.ResultsManifest, error) {
	if err := artifact.WriteOutputsManifest(rootDir, *outputs); err != nil {
		return artifact.ResultsManifest{}, fmt.Errorf("writing outputs manifest: %w", err)
	}

	results, err := rb.Build(outputs)
	if err != nil {
		return artifact.ResultsManifest{}, fmt.Errorf("building results manifest: %w", err)
	}
	if err := artifact.WriteResultsManifest(rootDir, results); err != nil {
		return artifact.ResultsManifest{}, fmt.Errorf("writing results manifest: %w", err)
	}

	return results, nil
}

// publishPhase is the v2 execution sink: it writes outputs.json and
// published.json from the in-memory OutputsManifest + ResultsBuilder owned
// by run.go. No Scratch reads. No publishManifest. No publish decision
// logic. The only inputs are the closure-captured `outputs` pointer (frozen
// intent snapshot, written by executePhase) and the `rb` (append-only
// outcome accumulator).
//
// Publication occurred if outputs has artifacts. There is no separate
// boolean tracking "did publication happen" — the OutputsManifest is the
// truth source for intent; rb is the truth source for what happened.
func publishPhase(outputs *artifact.OutputsManifest, rb *build.ResultsBuilder) pipeline.Phase {
	return pipeline.Phase{
		Name: "publish",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			if outputs == nil || len(outputs.Artifacts) == 0 {
				return &pipeline.PhaseResult{
					Name:    "publish",
					Status:  "skipped",
					Summary: "no artifacts",
				}, nil
			}

			results, err := writeManifests(pc.RootDir, outputs, rb)
			if err != nil {
				return nil, err
			}

			// Image references — UX printing, derived purely from outputs.
			// Intent-side data ("what was meant to be published"); a future
			// reader of the manifest sees the same shape.
			fmt.Fprintf(pc.Writer, "\n    Image References\n")
			for _, a := range outputs.Artifacts {
				if a.Kind != "docker" {
					continue
				}
				for _, t := range a.Targets {
					if t.Kind != "registry" || t.Registry == nil {
						continue
					}
					for _, tag := range t.Registry.Tags {
						fmt.Fprintf(pc.Writer, "    → %s/%s:%s\n",
							t.Registry.Host, t.Registry.Path, tag)
					}
				}
			}
			fmt.Fprintln(pc.Writer)

			outcomeCount := 0
			for _, r := range results.Results {
				outcomeCount += len(r.Outcomes)
			}
			return &pipeline.PhaseResult{
				Name:   "publish",
				Status: "success",
				Summary: fmt.Sprintf("%d outcome(s) across %d artifact(s)",
					outcomeCount, len(results.Results)),
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
