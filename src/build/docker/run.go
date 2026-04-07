package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/runner"
)

// Run is the entry point for docker build orchestration.
// It replaces the former runDockerBuild cobra handler body.
func Run(req Request) error {
	if req.Config == nil {
		return fmt.Errorf("docker.Run: config must not be nil")
	}
	if req.Context == nil {
		req.Context = context.Background()
	}
	if req.Stdout == nil {
		req.Stdout = os.Stdout
	}
	if req.Stderr == nil {
		req.Stderr = os.Stderr
	}

	if resolveBuildMode(req) == "crucible" {
		return runCrucibleMode(req)
	}

	// Inject project description for {project.description} templates
	if desc := postbuild.FirstDockerReadmeDescription(req.Config); desc != "" {
		gitver.SetProjectDescription(desc)
	}

	// Determine runner policy for this build.
	dockerRequired := false
	isCrucible := false
	for _, b := range req.Config.Builds {
		if b.Kind == "docker" {
			dockerRequired = true
		}
		if b.BuildMode == "crucible" {
			isCrucible = true
		}
	}

	pc := &pipeline.PipelineContext{
		Ctx:           req.Context,
		RootDir:       req.RootDir,
		Config:        req.Config,
		Writer:        req.Stdout,
		Color:         output.UseColor(),
		CI:            output.IsCI(),
		Verbose:       req.Verbose,
		SkipLint:      req.SkipLint,
		DryRun:        req.DryRun,
		Local:         req.Local,
		PipelineStart: time.Now(),
		Scratch:       make(map[string]any),
	}

	p := &pipeline.Pipeline{
		Phases: []pipeline.Phase{
			pipeline.BannerPhase(),
			pipeline.RunnerPreflightPhase(runner.Options{
				DockerRequired: dockerRequired,
				IsCrucible:     isCrucible,
			}),
			pipeline.LintPhase(),
			detectPhase(req),
			planPhase(req),
			pipeline.DryRunGate(renderPlan),
			cleanupPhase(),
			executePhase(req),
			publishPhase(),
			localCacheRetentionPhase(),
		},
		Hooks: []pipeline.PostBuildHook{
			postbuild.BadgeHook(req.Config, func(w io.Writer, color bool, rootDir string) (string, time.Duration) {
				return postbuild.RunBadgeSection(w, color, rootDir, req.Config)
			}),
			postbuild.ReadmeHook(),
			postbuild.RetentionHook(),
			externalCacheRetentionHook(),
		},
	}
	return p.Run(pc)
}
