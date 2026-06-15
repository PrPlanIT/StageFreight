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

// RepositoryModule is a module whose unit of analysis is the whole repository,
// not a single file. The engine invokes CheckRepository once over the repo root
// instead of fanning Check out per file. This is the honest contract for
// repo-scoped analyses (Flux graph render+validate, dependency scanning) that
// were previously forced through the per-file Module interface with a sync.Once
// guard — work attributed to an arbitrary file, with file-keyed caching that
// never matched what was actually analyzed.
//
// Repository findings may carry File == "" (the finding belongs to the repo, not
// a source line) or set File to a meaningful path (e.g. a build root) when one
// applies. Like Module, a RepositoryModule may also implement
// ToolchainAwareModule; the engine propagates toolchain config to both.
type RepositoryModule interface {
	Name() string
	CheckRepository(ctx context.Context, root string) ([]Finding, error)
	DefaultEnabled() bool
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
	registryMu   sync.RWMutex
	registry     = map[string]func() Module{}
	repoRegistry = map[string]func() RepositoryModule{}
)

// Register adds a module constructor to the global registry.
// Called from init() in each module file.
func Register(name string, constructor func() Module) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("lint: duplicate module registration: %s", name))
	}
	if _, exists := repoRegistry[name]; exists {
		panic(fmt.Sprintf("lint: name registered as both file and repository module: %s", name))
	}
	registry[name] = constructor
}

// RegisterRepository adds a repository-scoped module constructor to the registry.
// Called from init() in each repository module file. Names share one namespace
// with file modules so a name resolves unambiguously to exactly one kind.
func RegisterRepository(name string, constructor func() RepositoryModule) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := repoRegistry[name]; exists {
		panic(fmt.Sprintf("lint: duplicate repository module registration: %s", name))
	}
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("lint: name registered as both file and repository module: %s", name))
	}
	repoRegistry[name] = constructor
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

// GetRepository returns a new instance of the named repository module.
func GetRepository(name string) (RepositoryModule, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ctor, ok := repoRegistry[name]
	if !ok {
		return nil, fmt.Errorf("lint: unknown repository module: %s", name)
	}
	return ctor(), nil
}

// All returns sorted names of all registered file modules.
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

// AllRepository returns sorted names of all registered repository modules.
func AllRepository() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(repoRegistry))
	for name := range repoRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
