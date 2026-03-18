package build

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Engine is the interface every build engine implements.
type Engine interface {
	Name() string
	Detect(ctx context.Context, rootDir string) (*Detection, error)
	Plan(ctx context.Context, cfg interface{}, det *Detection) (*BuildPlan, error)
	Execute(ctx context.Context, plan *BuildPlan) (*BuildResult, error)
}

// ImagePlanInput bundles the config needed for image build planning.
type ImagePlanInput struct {
	Cfg     *config.Config // full config
	BuildID string         // optional: build specific entry by ID (empty = all)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]func() Engine{}
)

// Register adds an engine constructor to the global registry.
// Called from init() in each engine package.
func Register(name string, constructor func() Engine) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("build: duplicate engine registration: %s", name))
	}
	registry[name] = constructor
}

// Get returns a new instance of the named engine.
func Get(name string) (Engine, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("build: unknown engine: %s", name)
	}
	return ctor(), nil
}

// All returns sorted names of all registered engines.
func All() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
