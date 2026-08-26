// Package facts is StageFreight's single fact-resolution dispatch.
//
// Every templated surface — badge values, scribe bodies, notification text — resolves
// its {tokens} through one Registry instead of each surface keeping a private pass
// list. A Registry holds a set of Resolvers; each Resolver owns one or more fact
// families (the token prefixes it resolves) and declares the families it DEPENDS ON.
// The Registry applies them in topological order, so a fact whose value expands into
// another fact (e.g. {path.dockerhub} → a registry default_path → {org.handle}) is
// fully resolved in one Resolve call rather than left half-expanded.
//
// This package is the orchestrator: it may import the data packages (gitver, config,
// cistate). The dependency direction is one-way — surfaces import facts, facts imports
// the data packages, and the data packages import neither. In particular gitver never
// imports config; config-sourced inputs reach a resolver only through Context.
package facts

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// Context carries every input a resolver might read. A resolver reads only the fields
// its families need; the zero value is valid — a family whose inputs are absent
// resolves to empty or passes the token through, matching pre-registry behavior.
type Context struct {
	// Ctx scopes network calls made by live-data resolvers (registry/inventory).
	Ctx context.Context

	// Version and RootDir feed the gitver leaf families ({version}, {sha}, {commit.*},
	// {project.*}, scoped versions). RootDir "" disables git-dependent families.
	Version *gitver.VersionInfo
	RootDir string

	// Vars backs {var:name}; Description backs {project.description} (config-sourced,
	// injected explicitly rather than via any package global).
	Vars        map[string]string
	Description string

	// Config backs the identity/coordinate families and the registry/inventory
	// resolvers; State backs cistate run-identity and subsystem facts. Either may be nil.
	Config *config.Config
	State  *cistate.State
}

// Resolver rewrites a SET of templated values, resolving the fact families it owns.
// Operating on []string (rather than one string) is what lets batch resolvers —
// registry/inventory, which fetch each remote once across all values — sit behind the
// same interface as per-value resolvers, which simply map over the set. Resolve must
// be a pure function of (values, c) modulo idempotent live fetches, returning a slice
// the same length as its input, and must not mutate shared package state.
type Resolver interface {
	// Name identifies the resolver in diagnostics and ordering errors.
	Name() string
	// Provides lists the fact families this resolver resolves (token prefixes, e.g.
	// "path", "registry"; or the sentinel families a monolithic resolver owns).
	Provides() []string
	// DependsOn lists families that must be resolved before this resolver runs. A
	// family not provided by any registered resolver is treated as externally supplied
	// and imposes no ordering constraint.
	DependsOn() []string
	// Resolve returns values with this resolver's families expanded (same length).
	Resolve(values []string, c *Context) []string
}

// Registry applies its Resolvers in dependency order.
type Registry struct {
	resolvers []Resolver
}

// New returns an empty Registry.
func New() *Registry { return &Registry{} }

// Add registers a resolver. Registration order is the deterministic tiebreaker when
// two resolvers are not ordered by a dependency. Returns the registry for chaining.
func (r *Registry) Add(res Resolver) *Registry {
	r.resolvers = append(r.resolvers, res)
	return r
}

// Resolve applies every resolver to the value set in dependency order and returns the
// fully-expanded set (same length as values).
func (r *Registry) Resolve(values []string, c *Context) []string {
	for _, res := range r.ordered() {
		values = res.Resolve(values, c)
	}
	return values
}

// ResolveOne is the single-value convenience: resolve one string through the registry.
// Surfaces that render one body at a time (scribe) use this; batch surfaces (badges)
// call Resolve with the whole set so fetch-once resolvers work across it.
func (r *Registry) ResolveOne(value string, c *Context) string {
	return r.Resolve([]string{value}, c)[0]
}

// ordered returns the resolvers in dependency order: every resolver appears after all
// resolvers that provide a family it DependsOn. Implemented as a DFS post-order over
// the family-ownership graph, which yields dependencies-first and is deterministic in
// registration order. A dependency cycle is broken (the back-edge is ignored) rather
// than looping — the result is still a total order, just not a valid topo sort, which
// a construction-time check (future) will reject; for now cycles are a programming
// error the unit tests guard against.
func (r *Registry) ordered() []Resolver {
	familyOwner := make(map[string]int, len(r.resolvers))
	for i, res := range r.resolvers {
		for _, f := range res.Provides() {
			familyOwner[f] = i
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make([]int, len(r.resolvers))
	out := make([]Resolver, 0, len(r.resolvers))
	var visit func(i int)
	visit = func(i int) {
		if state[i] != unvisited {
			return // done, or a cycle back-edge (visiting) — break rather than loop
		}
		state[i] = visiting
		for _, dep := range r.resolvers[i].DependsOn() {
			if j, ok := familyOwner[dep]; ok && j != i {
				visit(j)
			}
		}
		state[i] = done
		out = append(out, r.resolvers[i])
	}
	for i := range r.resolvers {
		visit(i)
	}
	return out
}
