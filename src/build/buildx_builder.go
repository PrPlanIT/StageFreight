package build

import (
	"fmt"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// The image build (src/build/docker) owns the buildx builder and registers it here;
// engines reach it through this seam, since importing build/docker registers its contributors.
var (
	buildxBuilderMu sync.RWMutex
	buildxBuilder   func(config.BuilderConfig) (string, error)
)

// RegisterBuildxBuilder installs the function that ensures the configured buildx
// builder exists and is bootstrapped, returning its name.
func RegisterBuildxBuilder(fn func(config.BuilderConfig) (string, error)) {
	buildxBuilderMu.Lock()
	defer buildxBuilderMu.Unlock()
	buildxBuilder = fn
}

// EnsureBuildxBuilder ensures the configured buildx builder and returns its name, or why none is usable.
func EnsureBuildxBuilder(cfg config.BuilderConfig) (string, error) {
	buildxBuilderMu.RLock()
	fn := buildxBuilder
	buildxBuilderMu.RUnlock()
	if fn == nil {
		return "", fmt.Errorf("no buildx builder is registered in this binary")
	}
	return fn(cfg)
}
