package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// OrderedOrgs is the orgs: block — an id→org map (the map key becomes OrgConfig.ID).
// An org is identity ONLY: a maintainer and an open aliases map (the name-forms used
// to build coordinates on different surfaces). It carries no forge and no credentials
// — each surface owns its own credential, and a governance write token is DERIVED, not
// declared here. That identity≠credentials split is a hard invariant; see
// docs/design/identity-model.md.
type OrderedOrgs []OrgConfig

func (o *OrderedOrgs) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(c *OrgConfig, id string) { c.ID = id })
	if err != nil {
		return fmt.Errorf("orgs: %w", err)
	}
	*o = v
	return nil
}

// ByID returns the declared org with the given id.
func (o OrderedOrgs) ByID(id string) (OrgConfig, bool) {
	for _, c := range o {
		if c.ID == id {
			return c, true
		}
	}
	return OrgConfig{}, false
}

// OrgConfig is one organization/owner identity, keyed by its id in the orgs: map.
// Identity only — no forge, no credentials.
type OrgConfig struct {
	ID string `yaml:"-"` // from the map key

	// Maintainer is the "Name <email>" contact for this org's repos.
	Maintainer string `yaml:"maintainer,omitempty"`

	// Aliases is an OPEN map of name-forms used to build coordinates on different
	// surfaces — e.g. {handle: hlhd, lower: homelabhd, gitlab_group: "PrPlanIT/HomeLabHD"}.
	// A surface's default_path references one as {org.<alias>}. Common case is a single
	// entry; string→string only (a coordinate segment is always a scalar).
	Aliases map[string]string `yaml:"aliases,omitempty"`
}
