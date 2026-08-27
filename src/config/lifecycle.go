package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LifecycleConfig selects the repository lifecycle mode — the phase graph the
// pipeline runs. Mode is the single most significant orchestration choice:
// review and publish are image-only; the other modes mark them not_applicable.
type LifecycleConfig struct {
	// Preset references an external lifecycle fragment to inherit (the generic
	// preset: fragment-include mechanism — resolved and deep-merged before parse).
	Preset string `yaml:"preset,omitempty"`
	// Mode selects the phase graph. Empty defaults to image.
	//   image      — build → review → publish image pipeline (the default)
	//   docker     — read-only Docker plan/reconcile (dry-run)
	//   gitops     — validate + reconcile Flux manifests
	//   governance — governance control-repo reconcile
	Mode string `yaml:"mode"`
}

// GovernanceConfig declares governance profiles for the control repo. Presence of
// profiles activates governance reconcile (presence-gated like ansible/gitops).
// Assets (CI skeletons, settings files, etc.) are declared inside each profile's
// stagefreight config as assets: entries — no separate skeleton construct.
type GovernanceConfig struct {
	Profiles OrderedGovProfiles `yaml:"profiles"`
}

// OrderedGovProfiles is the governance.profiles: block — an id→profile map (key becomes
// GovernanceProfile.ID). Config-side this is used only for the presence gate (len>0);
// the governance package re-parses the same block richly for distribution.
type OrderedGovProfiles []GovernanceProfile

func (o *OrderedGovProfiles) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(p *GovernanceProfile, id string) { p.ID = id })
	if err != nil {
		return fmt.Errorf("profiles: %w", err)
	}
	*o = v
	return nil
}

func (OrderedGovProfiles) isIDMap() {}

// GovernanceProfile is one profile (config-side view). Repos and Config are raw maps —
// config only checks presence; the governance package owns the rich catalog shape.
type GovernanceProfile struct {
	ID          string         `yaml:"-"`                     // from the profiles: map key
	Repos       map[string]any `yaml:"repos,omitempty"`       // the location-anchored catalog (raw here)
	Config      map[string]any `yaml:"config,omitempty"`      // the profile's shared StageFreight config
	Credentials string         `yaml:"credentials,omitempty"` // env var prefix for the write token
}
