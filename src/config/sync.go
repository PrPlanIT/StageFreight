package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Sync — facet × scope replication grammar (repos.<id>.sync).
//
// A mirror repo declares which facets replicate from the primary and how much of
// each. Presence of a facet means "sync it"; absence means "don't". The block's
// own presence IS the mirror relationship — replication is one-directional
// (primary → this repo), source is the repo with roles:[primary].
//
// Two axes:
//
//	facet (what):     branches · tags · releases
//	scope (how much): current · all · exact
//
// The scope scalar is a PRESET over the facet map:
//
//	current → {scope: current}            incremental, never deletes
//	all     → {scope: all}                everything published, add-only
//	exact   → {scope: all, prune: true}   add + prune to match (faithful)
//
// Forms (equivalent grammar, increasing specificity):
//
//	sync: exact                                     whole-hog scalar (all facets)
//	sync: { branches: current, tags: all }          per-facet scalar
//	sync: { releases: {scope: all, assets: link} }  per-facet map (options)
//
// Omitting a facet means it is not synced. drafts is never auto-included by a
// preset — surfacing unpublished state is always deliberate.
const (
	scopeCurrent = "current"
	scopeAll     = "all"
	scopeExact   = "exact" // sugar: expands to {scope: all, prune: true}
)

// FacetSpec is the resolved replication spec for one facet. A nil *FacetSpec on
// SyncConfig means the facet is not synced.
type FacetSpec struct {
	Scope  string   `yaml:"scope,omitempty"`  // "current" | "all" (exact expands here)
	Prune  bool     `yaml:"prune,omitempty"`  // delete target refs/releases absent on source
	Drafts bool     `yaml:"drafts,omitempty"` // releases only: carry unpublished drafts
	Only   []string `yaml:"only,omitempty"`   // releases only: restrict to these tag-sources
	Match  string   `yaml:"match,omitempty"`  // glob filter on ref/tag name
	Assets string   `yaml:"assets,omitempty"` // releases only: "" | "true" | "false" | "link"
}

// SyncConfig is the per-facet replication spec on a mirror repo (repos.<id>.sync).
type SyncConfig struct {
	Branches *FacetSpec `yaml:"branches,omitempty"`
	Tags     *FacetSpec `yaml:"tags,omitempty"`
	Releases *FacetSpec `yaml:"releases,omitempty"`

	// legacy records that this block was written in the retired git/releases/docs
	// bool form and translated on load. Surfaced as a deprecation in the resolved
	// view; never serialized.
	legacy bool `yaml:"-"`
}

// IsCurrent reports scope: current — replicate only the ref the current run
// addresses (incremental, never deletes).
func (f *FacetSpec) IsCurrent() bool { return f != nil && f.Scope == scopeCurrent }

// IsAll reports scope: all — replicate every ref of the facet.
func (f *FacetSpec) IsAll() bool { return f != nil && f.Scope == scopeAll }

// SyncsGit reports whether this repo replicates git refs (branches or tags).
func (s SyncConfig) SyncsGit() bool { return s.Branches != nil || s.Tags != nil }

// SyncsReleases reports whether this repo replicates releases.
func (s SyncConfig) SyncsReleases() bool { return s.Releases != nil }

// IsLegacyForm reports whether the sync block was written in the retired
// git/releases/docs bool form (translated on load).
func (s SyncConfig) IsLegacyForm() bool { return s.legacy }

// Active reports whether any facet is synced.
func (s SyncConfig) Active() bool {
	return s.Branches != nil || s.Tags != nil || s.Releases != nil
}

// validateSyncFacets checks facet-scoped option placement beyond what the
// unmarshaler enforces: drafts and assets are releases-only options.
func validateSyncFacets(repoID string, s SyncConfig) []string {
	var errs []string
	check := func(name string, spec *FacetSpec) {
		if spec == nil {
			return
		}
		if spec.Drafts {
			errs = append(errs, fmt.Sprintf("repos[%s]: sync.%s: drafts is only valid on releases", repoID, name))
		}
		if spec.Assets != "" {
			errs = append(errs, fmt.Sprintf("repos[%s]: sync.%s: assets is only valid on releases", repoID, name))
		}
	}
	check("branches", s.Branches)
	check("tags", s.Tags)
	return errs
}

// scopePreset expands a scope scalar into a FacetSpec. "exact" flips prune;
// drafts is never auto-included.
func scopePreset(scope string) (*FacetSpec, error) {
	switch scope {
	case scopeCurrent:
		return &FacetSpec{Scope: scopeCurrent}, nil
	case scopeAll:
		return &FacetSpec{Scope: scopeAll}, nil
	case scopeExact:
		return &FacetSpec{Scope: scopeAll, Prune: true}, nil
	default:
		return nil, fmt.Errorf("unknown sync scope %q (want current|all|exact)", scope)
	}
}

// decodeFacet resolves one facet value: a scope scalar ("current"/"all"/"exact")
// or a map ({scope, prune, drafts, only, match, assets}). A map may seed from a
// scope word, with explicit peer options overriding the preset.
func decodeFacet(node *yaml.Node) (*FacetSpec, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		return scopePreset(node.Value)
	case yaml.MappingNode:
		var raw struct {
			Scope  string   `yaml:"scope"`
			Prune  *bool    `yaml:"prune"`
			Drafts *bool    `yaml:"drafts"`
			Only   []string `yaml:"only"`
			Match  string   `yaml:"match"`
			Assets string   `yaml:"assets"`
		}
		if err := node.Decode(&raw); err != nil {
			return nil, err
		}
		spec := &FacetSpec{Scope: scopeAll}
		if raw.Scope != "" {
			seeded, err := scopePreset(raw.Scope)
			if err != nil {
				return nil, err
			}
			spec = seeded
		}
		if raw.Prune != nil {
			spec.Prune = *raw.Prune
		}
		if raw.Drafts != nil {
			spec.Drafts = *raw.Drafts
		}
		if raw.Only != nil {
			spec.Only = raw.Only
		}
		if raw.Match != "" {
			spec.Match = raw.Match
		}
		if raw.Assets != "" {
			spec.Assets = raw.Assets
		}
		return spec, nil
	default:
		return nil, fmt.Errorf("sync facet must be a scope word or a map")
	}
}

// UnmarshalYAML accepts the whole-hog scalar (sync: exact), the per-facet map, and
// the retired git/releases/docs bool form (translated with behavior preserved).
func (s *SyncConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		spec, err := scopePreset(node.Value)
		if err != nil {
			return err
		}
		b, t, r := *spec, *spec, *spec
		s.Branches, s.Tags, s.Releases = &b, &t, &r
		return nil
	case yaml.MappingNode:
		return s.decodeMapping(node)
	default:
		return fmt.Errorf("sync must be a scope word or a facet map")
	}
}

func (s *SyncConfig) decodeMapping(node *yaml.Node) error {
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case "branches":
			spec, err := decodeFacet(val)
			if err != nil {
				return fmt.Errorf("sync.branches: %w", err)
			}
			s.Branches = spec
		case "tags":
			spec, err := decodeFacet(val)
			if err != nil {
				return fmt.Errorf("sync.tags: %w", err)
			}
			s.Tags = spec
		case "releases":
			// Disambiguate the retired bool form (releases: true) from the facet
			// form (releases: all | {scope: ...}) by the scalar's YAML tag.
			if val.Kind == yaml.ScalarNode && val.Tag == "!!bool" {
				s.legacy = true
				if val.Value == "true" {
					// Legacy release projection was add-only (no prune).
					s.Releases = &FacetSpec{Scope: scopeAll}
				}
				continue
			}
			spec, err := decodeFacet(val)
			if err != nil {
				return fmt.Errorf("sync.releases: %w", err)
			}
			s.Releases = spec
		case "git":
			// Legacy: git:true mirrored all heads+tags WITH unconditional prune.
			// Faithful translation is branches+tags exact (all + prune).
			s.legacy = true
			if val.Kind == yaml.ScalarNode && val.Tag == "!!bool" && val.Value == "true" {
				s.Branches = &FacetSpec{Scope: scopeAll, Prune: true}
				s.Tags = &FacetSpec{Scope: scopeAll, Prune: true}
			}
		case "docs":
			// Legacy: docs sync was never implemented (inert config). Dropped;
			// readme projection is a publish kind:metadata target, not sync.
			s.legacy = true
		default:
			return fmt.Errorf("sync: unknown key %q (want branches|tags|releases)", key)
		}
	}
	return nil
}
