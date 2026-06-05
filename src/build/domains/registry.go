package domains

import (
	"sort"
	"sync"
)

// The contributor registry mirrors build.RegisterV2 (engine_v2.go): a
// mutex-guarded set of factories, populated by init() in each contributor
// package. The run instantiates a fresh contributor per factory so per-run state
// (e.g. crucible's two-pass state) never leaks between runs.
var (
	registryMu sync.Mutex
	registry   []func() Contributor
)

// RegisterContributor adds a contributor factory to the global registry.
// Called from init() in each contributor package.
func RegisterContributor(factory func() Contributor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, factory)
}

// applicable instantiates a fresh contributor per registered factory, keeps the
// ones that Apply to this run, and sorts them by Order (stable, so equal orders
// keep registration order). Each strategy self-gates here, which is why the same
// registry serves perform (binary + crucible both apply), standalone
// `build binary` (only binary), and standalone `docker build` (only crucible).
func applicable(rc *RunContext) []Contributor {
	registryMu.Lock()
	factories := make([]func() Contributor, len(registry))
	copy(factories, registry)
	registryMu.Unlock()

	var active []Contributor
	for _, f := range factories {
		c := f()
		if len(rc.Only) > 0 && !contains(rc.Only, c.Name()) {
			continue
		}
		if c.Applies(rc) {
			active = append(active, c)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].Order() < active[j].Order() })
	return active
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
