package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// OrderedTargets is the publish: block — the distribution targets as an id→target
// map (the map key becomes each target's ID). Backed by a slice so execution
// order (YAML map order) is preserved. This is the ONLY accepted shape; the
// retired list form (targets:) upgrades through the config migrator, never here.
type OrderedTargets []TargetConfig

// expandMultiRegistryTargets fans a target declaring registry: [a, b, c] into N
// single-registry targets (id "<id>-<registry>"), so resolution and execution
// process exactly one registry per target and nothing downstream changes. A
// single- or no-registry target passes through unchanged (id preserved). Runs
// after validation (which sees the authored list) and before normalization.
func expandMultiRegistryTargets(targets OrderedTargets) OrderedTargets {
	// Fast path: nothing to fan.
	fan := false
	for _, t := range targets {
		if len(t.Registry) > 1 {
			fan = true
			break
		}
	}
	if !fan {
		return targets
	}
	out := make(OrderedTargets, 0, len(targets))
	for _, t := range targets {
		if len(t.Registry) <= 1 {
			out = append(out, t)
			continue
		}
		for _, rid := range t.Registry {
			clone := t // shallow copy — only Registry/ID are reassigned; shared slices/pointers stay read-only
			clone.Registry = StringOrList{rid}
			clone.ID = t.ID + "-" + rid
			out = append(out, clone)
		}
	}
	return out
}

// UnmarshalYAML decodes the id→target map in document order, stamping each
// target's ID from its key (strict-decoded — see decodeIDMap).
func (o *OrderedTargets) UnmarshalYAML(node *yaml.Node) error {
	v, err := decodeIDMap(node, func(t *TargetConfig, id string) { t.ID = id })
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	*o = v
	return nil
}
