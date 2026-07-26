package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RetentionPolicy defines how many tags/releases to keep using time-bucketed rules.
// Policies are additive — a tag survives if ANY rule wants to keep it.
// This mirrors restic's forget policy.
type RetentionPolicy struct {
	KeepLast     int      `yaml:"keep_last"`     // keep the N most recent tags per series (0/-1/unset = ∞)
	KeepDaily    int      `yaml:"keep_daily"`    // keep one per day for the last N days
	KeepWeekly   int      `yaml:"keep_weekly"`   // keep one per week for the last N weeks
	KeepMonthly  int      `yaml:"keep_monthly"`  // keep one per month for the last N months
	KeepYearly   int      `yaml:"keep_yearly"`   // keep one per year for the last N years
	KeepBranches int      `yaml:"keep_branches"` // keep the N most-recent identity groups per template (bounds retired branches; 0/unset = ∞)
	Identity     []string `yaml:"identity"`      // extra identity vars beyond the {branch}/{env} defaults — partition tags into independent series
	Protect      []string `yaml:"protect"`       // tag patterns that are never deleted, an explicit override
}

// Active returns true if any retention rule is configured. keep_last/buckets are
// active only when positive (0/-1/unset all mean ∞ / keep-all); keep_branches is a
// group-count bound that is likewise only active when positive.
func (r RetentionPolicy) Active() bool {
	return r.KeepLast > 0 || r.KeepDaily > 0 || r.KeepWeekly > 0 || r.KeepMonthly > 0 || r.KeepYearly > 0 || r.KeepBranches > 0
}

// UnmarshalYAML implements custom unmarshaling so retention accepts both:
//
//	retention: 10          → RetentionPolicy{KeepLast: 10}
//	retention:
//	  keep_last: 3
//	  keep_daily: 7        → RetentionPolicy{KeepLast: 3, KeepDaily: 7}
func (r *RetentionPolicy) UnmarshalYAML(value *yaml.Node) error {
	// Try scalar (int) first
	if value.Kind == yaml.ScalarNode {
		var n int
		if err := value.Decode(&n); err != nil {
			return fmt.Errorf("retention: expected integer or policy map, got %q", value.Value)
		}
		r.KeepLast = n
		return nil
	}

	// Try map
	if value.Kind == yaml.MappingNode {
		// Decode into an alias type to avoid infinite recursion
		type policyAlias RetentionPolicy
		var alias policyAlias
		if err := value.Decode(&alias); err != nil {
			return fmt.Errorf("retention: %w", err)
		}
		*r = RetentionPolicy(alias)
		return nil
	}

	return fmt.Errorf("retention: expected integer or map, got YAML kind %d", value.Kind)
}
