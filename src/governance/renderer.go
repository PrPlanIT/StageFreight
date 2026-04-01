package governance

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RenderSealedConfig produces a sealed .stagefreight.yml for a satellite repo.
// The seal is a human-facing warning header with provenance — not governance state.
// Uses canonical key ordering for deterministic, diff-stable output.
func RenderSealedConfig(seal SealMeta, config map[string]any) ([]byte, error) {
	var b strings.Builder

	// Seal header.
	b.WriteString("# ------------------------------------------------------------------------------\n")
	b.WriteString("# GENERATED / ENFORCED BY STAGEFREIGHT GOVERNANCE\n")
	b.WriteString(fmt.Sprintf("# Source repo: %s\n", seal.SourceRepo))
	b.WriteString(fmt.Sprintf("# Source ref: %s\n", seal.SourceRef))
	b.WriteString(fmt.Sprintf("# Cluster: %s\n", seal.ClusterID))
	b.WriteString("# This .stagefreight.yml is governed, not purely local.\n")
	b.WriteString("# To disable governance, detach from the control repo workflow.\n")
	b.WriteString("# Manual changes may be overwritten by reconciliation.\n")
	b.WriteString("# ------------------------------------------------------------------------------\n")
	b.WriteString("\n")

	// Render config in canonical key order.
	body, err := renderCanonical(config)
	if err != nil {
		return nil, fmt.Errorf("rendering sealed config: %w", err)
	}
	b.Write(body)

	return []byte(b.String()), nil
}

// SealMeta holds provenance info for the generated header.
type SealMeta struct {
	SourceRepo string // e.g., "https://gitlab.prplanit.com/PrPlanIT/MaintenancePolicy"
	SourceRef  string // e.g., "v1.0.0" or commit SHA
	ClusterID  string // e.g., "docker-services"
}

// RenderEffective returns config as YAML with canonical key ordering.
// Used by `config render` for display.
func RenderEffective(config map[string]any) ([]byte, error) {
	return renderCanonical(config)
}

// renderCanonical serializes a config map with fixed top-level key order.
// Keys not in CanonicalKeyOrder are appended alphabetically at the end.
func renderCanonical(config map[string]any) ([]byte, error) {
	node := &yaml.Node{
		Kind: yaml.MappingNode,
	}

	// First: canonical order.
	added := make(map[string]bool)
	for _, key := range CanonicalKeyOrder {
		val, ok := config[key]
		if !ok {
			continue
		}
		added[key] = true
		if err := appendKeyValue(node, key, val); err != nil {
			return nil, err
		}
	}

	// Then: any remaining keys (alphabetical).
	remaining := make([]string, 0)
	for k := range config {
		if !added[k] {
			remaining = append(remaining, k)
		}
	}
	sortStrings(remaining)
	for _, key := range remaining {
		if err := appendKeyValue(node, key, config[key]); err != nil {
			return nil, err
		}
	}

	doc := &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{node},
	}

	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	enc.Close()

	return []byte(b.String()), nil
}

func appendKeyValue(node *yaml.Node, key string, val any) error {
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}

	valNode := &yaml.Node{}
	valBytes, err := yaml.Marshal(val)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", key, err)
	}
	if err := yaml.Unmarshal(valBytes, valNode); err != nil {
		return fmt.Errorf("unmarshaling %s node: %w", key, err)
	}

	if valNode.Kind == yaml.DocumentNode && len(valNode.Content) > 0 {
		valNode = valNode.Content[0]
	}

	node.Content = append(node.Content, keyNode, valNode)
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
