package lint

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Module is the interface every lint check implements.
type Module interface {
	Name() string
	Check(ctx context.Context, file FileInfo) ([]Finding, error)
	DefaultEnabled() bool
	AutoDetect() []string // glob patterns that trigger auto-enable
}

// ConfigurableModule is implemented by modules that accept YAML options.
// The engine calls Configure after construction if the module's config
// section contains an options map.
type ConfigurableModule interface {
	Module
	Configure(opts map[string]any) error
}

// ToolchainAwareModule is implemented by modules that need toolchain config
// for version pinning. The engine calls SetToolchainDesired after construction
// if the module implements this interface.
type ToolchainAwareModule interface {
	Module
	SetToolchainDesired(desired map[string]config.ToolPinConfig)
}

// CacheTTLModule controls time-based cache expiry.
//
// Modules that do not implement this interface are cached forever.
//
// Semantics:
//
//	>0  → cache with expiry (e.g. 5*time.Minute)
//	 0  → cache forever (content-hash only)
//	<0  → never cache (always re-run)
type CacheTTLModule interface {
	CacheTTL() time.Duration
}

var (
	registryMu sync.RWMutex
	registry   = map[string]func() Module{}
)

// Register adds a module constructor to the global registry.
// Called from init() in each module file.
func Register(name string, constructor func() Module) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("lint: duplicate module registration: %s", name))
	}
	registry[name] = constructor
}

// Get returns a new instance of the named module.
func Get(name string) (Module, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("lint: unknown module: %s", name)
	}
	return ctor(), nil
}

// All returns sorted names of all registered modules.
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
