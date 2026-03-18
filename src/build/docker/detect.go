package docker

import (
	"fmt"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// detectPhase discovers Dockerfiles and the repo language.
func detectPhase(req Request) pipeline.Phase {
	return pipeline.Phase{
		Name: "detect",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			output.SectionStartCollapsed(pc.Writer, "sf_detect", "Detect")
			detectStart := time.Now()

			engine, err := build.Get("image")
			if err != nil {
				output.SectionEnd(pc.Writer, "sf_detect")
				return nil, err
			}
			pc.Scratch["docker.engine"] = engine

			det, err := engine.Detect(pc.Ctx, pc.RootDir)
			if err != nil {
				output.SectionEnd(pc.Writer, "sf_detect")
				return nil, fmt.Errorf("detection: %w", err)
			}
			pc.Scratch["docker.det"] = det

			detectElapsed := time.Since(detectStart)

			detectSec := output.NewSection(pc.Writer, "Detect", detectElapsed, pc.Color)
			for _, df := range det.Dockerfiles {
				detectSec.Row("%-16s→ %s", "Dockerfile", df.Path)
			}
			detectSec.Row("%-16s→ %s (auto-detected)", "language", det.Language)
			detectSec.Row("%-16s→ %s", "context", ".")
			if req.Target != "" {
				detectSec.Row("%-16s→ %s", "target", req.Target)
			} else {
				detectSec.Row("%-16s→ %s", "target", "(default)")
			}
			detectSec.Close()
			output.SectionEnd(pc.Writer, "sf_detect")

			summary := fmt.Sprintf("%d Dockerfile(s), %s", len(det.Dockerfiles), det.Language)
			return &pipeline.PhaseResult{
				Name:    "detect",
				Status:  "success",
				Summary: summary,
				Elapsed: detectElapsed,
			}, nil
		},
	}
}
