package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RetentionPolicy defines how many tags/releases to keep using time-bucketed rules.
// Policies are additive — a tag survives if ANY rule wants to keep it.
// This mirrors restic's forget policy.
type RetentionPolicy struct {
	KeepLast     int            `yaml:"keep_last"`     // keep the N most recent tags per series (0/-1/unset = ∞)
	KeepDaily    int            `yaml:"keep_daily"`    // keep one per day for the last N days
	KeepWeekly   int            `yaml:"keep_weekly"`   // keep one per week for the last N weeks
	KeepMonthly  int            `yaml:"keep_monthly"`  // keep one per month for the last N months
	KeepYearly   int            `yaml:"keep_yearly"`   // keep one per year for the last N years
	KeepBranches int            `yaml:"keep_branches"` // keep the N most-recent identity groups per template (bounds retired branches; 0/unset = ∞)
	MaxAge       string         `yaml:"max_age"`       // keep items newer than this ("14d", "72h"); additive like keep_* — alone it means "only the recent survive"
	Identity     []string       `yaml:"identity"`      // extra identity vars beyond the {branch}/{env} defaults — partition tags into independent series
	Protect      []string       `yaml:"protect"`       // tag patterns that are never deleted, an explicit override
	Refs         []RetentionRef `yaml:"refs"`          // scoped overrides: a different policy for items matching a pattern (first match wins; unset fields inherit this default)
}

// RetentionRef is one scoped retention override: items matching Match are governed by
// this rule set instead of the default policy. Fields left unset inherit the default's
// value — a ref narrows or widens specific rules without restating the whole policy.
// The SAME grammar applies at every retention site (publish targets, toolchains,
// declared image eviction, releases, packages, mirrors) — one engine implements it.
type RetentionRef struct {
	Match       string `yaml:"match"` // pattern (same syntax as protect/templates)
	KeepLast    int    `yaml:"keep_last"`
	KeepDaily   int    `yaml:"keep_daily"`
	KeepWeekly  int    `yaml:"keep_weekly"`
	KeepMonthly int    `yaml:"keep_monthly"`
	KeepYearly  int    `yaml:"keep_yearly"`
	MaxAge      string `yaml:"max_age"`
}

// Active returns true if any retention rule is configured. keep_last/buckets are
// active only when positive (0/-1/unset all mean ∞ / keep-all); keep_branches is a
// group-count bound that is likewise only active when positive. max_age and any
// scoped ref rule likewise activate the policy.
func (r RetentionPolicy) Active() bool {
	if r.KeepLast > 0 || r.KeepDaily > 0 || r.KeepWeekly > 0 || r.KeepMonthly > 0 || r.KeepYearly > 0 || r.KeepBranches > 0 || r.MaxAge != "" {
		return true
	}
	for _, ref := range r.Refs {
		if ref.KeepLast > 0 || ref.KeepDaily > 0 || ref.KeepWeekly > 0 || ref.KeepMonthly > 0 || ref.KeepYearly > 0 || ref.MaxAge != "" {
			return true
		}
	}
	return false
}

// Ref-scoped resolution (which ref governs a given item name) lives in the retention
// engine (retention.Effective) — it needs the template→pattern compiler, and the config
// package stays pure data.

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
