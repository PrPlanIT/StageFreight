package docker

import (
	"context"
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
)

// Run is the entry point for standalone `stagefreight docker build`: it routes
// to the domain spine via the image or crucible contributor.
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

	// Inject project description for {project.description} templates
	if desc := postbuild.FirstDockerReadmeDescription(req.Config); desc != "" {
		gitver.SetProjectDescription(desc)
	}

	only := "image"
	if resolveBuildMode(req) == "crucible" {
		only = "docker"
	}
	return domains.Run(dockerRunContext(req, only))
}

// dockerRunContext adapts a docker Request into a domains.RunContext. Only
// constrains the run to one contributor (image or crucible); the build-selection
// flags pass straight through.
func dockerRunContext(req Request, only string) *domains.RunContext {
	return &domains.RunContext{
		Ctx:       req.Context,
		RootDir:   req.RootDir,
		Config:    req.Config,
		Writer:    req.Stdout,
		Stderr:    req.Stderr,
		Color:     output.UseColor(),
		Verbose:   req.Verbose,
		DryRun:    req.DryRun,
		Store:     req.Store,
		Local:     req.Local,
		Platforms: req.Platforms,
		BuildID:   req.BuildID,
		Target:    req.Target,
		Only:      []string{only},
		Outputs:   &artifact.OutputsManifest{},
		RB:        build.NewResultsBuilder(),
	}
}
