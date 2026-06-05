package docker

import (
	"context"
	"io"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/cas"
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

	// Store is the content-addressed artifact store that retains build bytes for
	// cross-phase transport. Transport is a mandatory part of the deployment
	// lifecycle: the CI perform stage always supplies a real store (FSStore
	// today), so review and publish operate on the exact reviewed bytes. FSStore
	// is merely the current implementation — the guarantee is mandatory, the
	// backing store is replaceable (registry-/object-storage-backed CAS may
	// follow) behind this interface.
	//
	// A nil Store falls back to cas.NewNoopStore() purely as memory-safety for
	// direct docker.Run callers outside the lifecycle (e.g. the standalone
	// `docker build` CLI). Nil is NOT a supported way to disable transport in a
	// deployment pipeline — there is intentionally no config knob for that.
	Store cas.Store

	// Embedded marks this docker run as one contribution to the perform domain
	// spine (which runs the binary engine + the docker engine as one cohesive
	// run). When true, the docker run does NOT render its own banner/Code,
	// executor, or Summary box — the spine renders identity/executor once and
	// owns the single merged Summary. The Crucible Verdict still renders inline.
	Embedded bool

	// ResultSink, when non-nil, receives this run's phase results (lint, detect,
	// plan, build, verification, publish) instead of the docker run rendering its
	// own Summary box. The spine appends these to the binary engine's results and
	// renders one Summary for the whole perform run. Only consulted when Embedded.
	ResultSink *[]pipeline.PhaseResult
}
