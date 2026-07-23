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

// UnmarshalYAML decodes the id→target map in document order, stamping each
// target's ID from its key.
func (o *OrderedTargets) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("publish: must be a map of id → target")
	}
	out := make([]TargetConfig, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var tc TargetConfig
		if err := node.Content[i+1].Decode(&tc); err != nil {
			return fmt.Errorf("publish.%s: %w", key, err)
		}
		tc.ID = key
		out = append(out, tc)
	}
	*o = out
	return nil
}
