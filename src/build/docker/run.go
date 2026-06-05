package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
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
		// Converged: standalone crucible `docker build` runs through the SAME
		// domain spine + crucible contributor as perform — one orchestration, one
		// crucible implementation. `docker build` is just a constrained invocation
		// of that engine (Only:["docker"] + the build-selection flags from req),
		// not a second orchestrator. The legacy runCrucibleMode stays defined as a
		// recoverable fallback until this converged path has a green run behind it;
		// it is deleted in the follow-up (Commit B).
		return domains.Run(crucibleRunContext(req))
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

	// Shared state between executePhase and publishPhase, captured by both
	// phase closures. Replaces the pc.Scratch handoff. outputs is populated
	// once by executePhase from the resolved BuildPlan; rb is append-only
	// and the sole accumulator of push/attestation outcomes.
	var outputs artifact.OutputsManifest
	rb := build.NewResultsBuilder()

	p := &pipeline.Pipeline{
		Phases: []pipeline.Phase{
			pipeline.BannerPhase(),
			pipeline.ExecutorPreflightPhase(runner.Options{
				DockerRequired: dockerRequired,
				IsCrucible:     isCrucible,
			}),
			pipeline.LintPhase(),
			detectPhase(req),
			planPhase(req),
			pipeline.DryRunGate(renderPlan),
			cleanupPhase(),
			executePhase(req, &outputs, rb),
			publishPhase(&outputs, rb),
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

// crucibleRunContext adapts a docker Request into a domains.RunContext for the
// converged crucible path. It is a PURE mapping — Only:["docker"] constrains the
// run to the crucible contributor, and the build-selection flags pass straight
// through — so standalone `docker build` is a constrained invocation of the one
// lifecycle engine, not a second orchestrator hiding behind docker.Run.
func crucibleRunContext(req Request) *domains.RunContext {
	return &domains.RunContext{
		Ctx:       req.Context,
		RootDir:   req.RootDir,
		Config:    req.Config,
		Writer:    req.Stdout,
		Stderr:    req.Stderr,
		Color:     output.UseColor(),
		Verbose:   req.Verbose,
		SkipLint:  req.SkipLint,
		DryRun:    req.DryRun,
		Store:     req.Store,
		Local:     req.Local,
		Platforms: req.Platforms,
		BuildID:   req.BuildID,
		Target:    req.Target,
		Only:      []string{"docker"},
		Outputs:   &artifact.OutputsManifest{},
		RB:        build.NewResultsBuilder(),
	}
}
