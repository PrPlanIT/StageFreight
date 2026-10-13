package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// unusedMatcherWarnings flags branch matchers the repo declares itself but nothing uses:
// almost always a typo or leftover. A preset's matchers are shared vocabulary that any
// given repo may not need, so only the authored names are checked.
func unusedMatcherWarnings(cfg *Config, authored []string) []string {
	used := map[string]bool{}
	for _, bb := range cfg.Git.Versioning.BranchBuilds {
		if bb.Match != "" {
			used[bb.Match] = true
		}
	}
	markWhen := func(when WhenConditions) {
		for _, c := range when {
			for _, b := range c.Branches {
				if !strings.HasPrefix(b, "re:") && isIdentifier(b) {
					used[b] = true
				}
			}
		}
	}
	for _, t := range cfg.Targets {
		markWhen(t.When)
	}
	for _, n := range cfg.Notifications {
		markWhen(n.When)
	}

	var warnings []string
	for _, name := range authored {
		if !used[name] {
			warnings = append(warnings, fmt.Sprintf("git.branches.%s is defined but nothing uses it (builds, targets, notifications)", name))
		}
	}
	return warnings
}

// mappingKeys returns the sorted keys of the mapping at path in the document as written,
// before presets merge into it.
func mappingKeys(root *yaml.Node, path ...string) []string {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, key := range path {
		if n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	sort.Strings(keys)
	return keys
}
