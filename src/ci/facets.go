package ci

import (
	"github.com/PrPlanIT/StageFreight/src/config"
)

// FacetDef defines a CI facet — a capability that may be active in a repo.
// Predicates are deterministic: config presence only, no heuristics.
type FacetDef struct {
	Name      string                      // "build", "security", "gitops-reconcile", etc.
	Subsystem string                      // what `ci run <x>` dispatches to
	NeedsDinD bool                        // requires Docker-in-Docker transport
	Predicate func(*config.Config) bool   // config-driven activation check
}

// ActiveFacet is a facet that passed its predicate.
type ActiveFacet struct {
	Name      string
	Subsystem string
	NeedsDinD bool
}

// FacetRegistry is the canonical, ordered set of all known facets.
// Order determines canonical stage ordering.
var FacetRegistry = []FacetDef{
	{
		Name:      "validate",
		Subsystem: "validate",
		NeedsDinD: false,
		Predicate: func(c *config.Config) bool { return c.Lint.Level != "" },
	},
	{
		Name:      "deps",
		Subsystem: "deps",
		NeedsDinD: true,
		Predicate: func(c *config.Config) bool { return c.Dependency.Enabled },
	},
	{
		Name:      "build",
		Subsystem: "build",
		NeedsDinD: true,
		Predicate: func(c *config.Config) bool { return len(c.Builds) > 0 },
	},
	{
		Name:      "security",
		Subsystem: "security",
		NeedsDinD: true,
		Predicate: func(c *config.Config) bool { return c.Security.Enabled },
	},
	{
		Name:      "release",
		Subsystem: "release",
		NeedsDinD: false,
		Predicate: func(c *config.Config) bool { return c.Release.Enabled },
	},
	{
		Name:      "gitops-reconcile",
		Subsystem: "reconcile",
		NeedsDinD: false,
		Predicate: func(c *config.Config) bool { return c.GitOps.Cluster.Name != "" },
	},
	{
		Name:      "governance-reconcile",
		Subsystem: "reconcile",
		NeedsDinD: false,
		Predicate: func(c *config.Config) bool { return len(c.Governance.Clusters) > 0 },
	},
	{
		Name:      "docs",
		Subsystem: "docs",
		NeedsDinD: false,
		Predicate: func(c *config.Config) bool { return c.Docs.Enabled },
	},
}

// DetectActive evaluates all facets against the effective config.
// Returns only active facets in canonical order.
func DetectActive(cfg *config.Config) []ActiveFacet {
	var active []ActiveFacet
	for _, f := range FacetRegistry {
		if f.Predicate(cfg) {
			active = append(active, ActiveFacet{
				Name:      f.Name,
				Subsystem: f.Subsystem,
				NeedsDinD: f.NeedsDinD,
			})
		}
	}
	return active
}

// NeedsDinD returns true if any active facet requires Docker-in-Docker.
func NeedsDinD(facets []ActiveFacet) bool {
	for _, f := range facets {
		if f.NeedsDinD {
			return true
		}
	}
	return false
}

// HasFacet checks if a named facet is in the active set.
func HasFacet(facets []ActiveFacet, name string) bool {
	for _, f := range facets {
		if f.Name == name {
			return true
		}
	}
	return false
}

