package docker

import (
	"context"
	"io"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Request holds all inputs for a docker build pipeline run.
// Every field that previously came from a package-global variable is explicitly
// passed here, eliminating hidden coupling to cobra flag state.
type Request struct {
	Context    context.Context
	RootDir    string
	Config     *config.Config
	Verbose    bool
	Local      bool
	Platforms  []string
	Tags       []string
	Target     string
	BuildID    string
	SkipLint   bool
	DryRun     bool
	BuildMode  string
	ConfigFile string // forwarded by crucible to inner build
	Stdout     io.Writer
	Stderr     io.Writer
}
