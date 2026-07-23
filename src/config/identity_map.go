package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// strictNodeDecode decodes a single YAML node into v with KnownFields(true), so an
// unknown/typo'd field inside a map-keyed entry is an error rather than silently
// dropped. yaml.Node.Decode itself can't carry the parent decoder's strictness, so
// we round-trip the node through a strict decoder. Used by the keyed-map forms
// (identity graph, builds, publish) whose custom unmarshalers would otherwise
// decode entry values leniently.
func strictNodeDecode(node *yaml.Node, v any) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// decodeIDMap decodes an id→entry YAML map into an ordered slice, stamping each
// entry's ID from its key (via setID). Document order is preserved, and each entry
// is strict-decoded. This is the shared machinery behind the keyed-map form of the
// identity graph, builds, and publish — the ONLY accepted shape; the retired list
// form upgrades through the migrator.
func decodeIDMap[T any](node *yaml.Node, setID func(*T, string)) ([]T, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("must be an id → entry map")
	}
	out := make([]T, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		var v T
		if err := strictNodeDecode(node.Content[i+1], &v); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		setID(&v, key)
		out = append(out, v)
	}
	return out, nil
}

// OrderedForges is the forges: block — an id→forge map (key becomes ForgeConfig.ID).
type OrderedForges []ForgeConfig

func (o *OrderedForges) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(f *ForgeConfig, id string) { f.ID = id })
	if err != nil {
		return fmt.Errorf("forges: %w", err)
	}
	*o = v
	return nil
}

// OrderedRepos is the repos: block — an id→repo map (key becomes RepoConfig.ID).
type OrderedRepos []RepoConfig

func (o *OrderedRepos) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(r *RepoConfig, id string) { r.ID = id })
	if err != nil {
		return fmt.Errorf("repos: %w", err)
	}
	*o = v
	return nil
}

// OrderedRegistries is the registries: block — an id→registry map (key becomes
// RegistryConfig.ID).
type OrderedRegistries []RegistryConfig

func (o *OrderedRegistries) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(r *RegistryConfig, id string) { r.ID = id })
	if err != nil {
		return fmt.Errorf("registries: %w", err)
	}
	*o = v
	return nil
}

// OrderedBuilds is the builds: block — an id→build map (key becomes BuildConfig.ID).
type OrderedBuilds []BuildConfig

func (o *OrderedBuilds) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(b *BuildConfig, id string) { b.ID = id })
	if err != nil {
		return fmt.Errorf("builds: %w", err)
	}
	*o = v
	return nil
}

// OrderedSigningProfiles is signing.profiles — an id→profile map (key becomes
// SigningProfile.ID).
type OrderedSigningProfiles []SigningProfile

func (o *OrderedSigningProfiles) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(p *SigningProfile, id string) { p.ID = id })
	if err != nil {
		return fmt.Errorf("signing.profiles: %w", err)
	}
	*o = v
	return nil
}
