package runtime

import (
	"fmt"
	"sort"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = map[string]map[string]func() LifecycleBackend{}
)

// Register adds a backend constructor to the global registry.
// Called from init() in each backend package.
// Key is (mode, name) — e.g. ("gitops", "flux").
func Register(mode, name string, constructor func() LifecycleBackend) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if registry[mode] == nil {
		registry[mode] = map[string]func() LifecycleBackend{}
	}
	if _, exists := registry[mode][name]; exists {
		panic(fmt.Sprintf("runtime: duplicate backend registration: %s/%s", mode, name))
	}
	registry[mode][name] = constructor
}

// ResolveBackend selects a backend by mode and name.
// Returns hard error for unknown/unimplemented combinations. Never silent fallback.
func ResolveBackend(mode, name string) (LifecycleBackend, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	backends, ok := registry[mode]
	if !ok {
		return nil, fmt.Errorf("unknown lifecycle mode: %q (registered modes: %s)",
			mode, registeredModes())
	}

	ctor, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("unknown backend %q for mode %q (registered backends: %s)",
			name, mode, registeredBackends(mode))
	}

	return ctor(), nil
}

func registeredModes() string {
	modes := make([]string, 0, len(registry))
	for m := range registry {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	return fmt.Sprintf("[%s]", joinStrings(modes))
}

func registeredBackends(mode string) string {
	backends, ok := registry[mode]
	if !ok {
		return "[]"
	}
	names := make([]string, 0, len(backends))
	for n := range backends {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf("[%s]", joinStrings(names))
}

func joinStrings(s []string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += ", "
		}
		result += v
	}
	return result
}
