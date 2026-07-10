package lint

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
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

// SnapshotAwareModule is implemented by modules that can consume a
// pre-resolved supplychain.Snapshot instead of resolving dependencies
// themselves. The engine calls SetSnapshot after construction, before Run(),
// if the module implements this interface AND a Snapshot was provided by the
// caller (e.g. the audition pipeline, which resolves once via
// discovery.Discover and shares the result across lint and dependency-update
// rather than resolving per-consumer). Mirrors ToolchainAwareModule.
//
// When no Snapshot is provided (Engine.Snapshot is nil), modules implementing
// this interface must fall back to on-demand resolution — this keeps
// standalone `stagefreight lint` working.
type SnapshotAwareModule interface {
	Module
	SetSnapshot(snapshot *supplychain.Snapshot)
}

// WholeRepoModule is implemented by modules that analyze the ENTIRE file set in
// one pass instead of once per file. When a module implements this interface the
// engine invokes CheckAll exactly once — with every eligible file (engine-wide
// and this module's own excludes already applied) — and NEVER calls Check on it.
//
// This is the seam for cross-file analyses that a per-file Check cannot express:
// canonical-vulnerability dedup across a manifest and its lockfile (which are
// DIFFERENT files, so a per-file reduce sees only one leg of each advisory), or
// an external whole-repo linter (e.g. golangci-lint) that owns its own file
// walking and reports over a whole module at once.
//
// A whole-repo module still satisfies Module so it registers, configures, and
// auto-detects identically. Its Check is a mis-dispatch guard: because the
// engine routes whole-repo modules to CheckAll, a call to Check means something
// bypassed that dispatch, and the module should fail loud rather than silently
// emit nothing.
type WholeRepoModule interface {
	Module
	CheckAll(ctx context.Context, files []FileInfo) ([]Finding, error)
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
