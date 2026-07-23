package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// WhenConditions is a target's routing gate: one condition-set, or a list of them
// combined with OR — the target fires if ANY condition matches. It accepts either
// a single condition map (when: { ... }) or a sequence of them (when: [ {...}, {...} ]).
// An empty/absent when: is unconditional (eligible in every context).
//
// All when: interpretation flows through TargetEligibility (target_match.go); this
// type only models the shape.
type WhenConditions []TargetCondition

// UnmarshalYAML accepts a single condition map OR a sequence of condition maps.
func (w *WhenConditions) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		var c TargetCondition
		if err := node.Decode(&c); err != nil {
			return err
		}
		*w = WhenConditions{c}
	case yaml.SequenceNode:
		var list []TargetCondition
		if err := node.Decode(&list); err != nil {
			return err
		}
		*w = list
	default:
		return fmt.Errorf("when: must be a condition map or a list of condition maps")
	}
	return nil
}
