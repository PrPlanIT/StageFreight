package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// GitConfig is the git: cluster — how the engine interprets a ref into named
// branch/tag patterns and the versions they imply (was matchers + versioning,
// merged into one cohesive block). It is the SINGLE source: consumers read
// cfg.Git.Branches / cfg.Git.Tags / cfg.Git.Versioning directly — no translation
// layer, no separate Matchers/Versioning types.
type GitConfig struct {
	// Branches maps a matcher name to a regex (was matchers.branches).
	Branches map[string]string `yaml:"branches,omitempty"`
	// Tags maps a tag-source name to its pattern (was versioning.tag_sources list).
	Tags OrderedTagSources `yaml:"tags,omitempty"`
	// Versioning holds the derivation rules that consume the patterns above.
	Versioning GitVersioning `yaml:"versioning,omitempty"`
}

// GitVersioning is git.versioning — branch_builds + no_lineage (tag patterns moved
// up to git.tags).
type GitVersioning struct {
	BranchBuilds OrderedBranchBuilds `yaml:"branch_builds,omitempty"`
	NoLineage    NoLineageConfig     `yaml:"no_lineage,omitempty"`
}

// OrderedTagSources is git.tags — an id→{pattern} map (key becomes TagSourceConfig.ID).
type OrderedTagSources []TagSourceConfig

func (o *OrderedTagSources) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(t *TagSourceConfig, id string) { t.ID = id })
	if err != nil {
		return fmt.Errorf("git.tags: %w", err)
	}
	*o = v
	return nil
}

// OrderedBranchBuilds is git.versioning.branch_builds — an id→rule map (key becomes
// BranchBuildConfig.ID).
type OrderedBranchBuilds []BranchBuildConfig

func (o *OrderedBranchBuilds) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(b *BranchBuildConfig, id string) { b.ID = id })
	if err != nil {
		return fmt.Errorf("git.versioning.branch_builds: %w", err)
	}
	*o = v
	return nil
}
