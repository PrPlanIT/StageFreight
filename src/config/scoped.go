package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Scoped[T] is a value that is either an inferred DEFAULT (the common case) or scoped
// per surface — the same override idiom as `names`, for content values (description,
// readme). A plain value is the default used on every un-named surface; a map form
//
//	{ default: <T>, <surface>: <T>, … }
//
// overrides specific surfaces. Resolution at surface S is BySurface[S] if present, else
// Default.
//
// Disambiguation is by YAML shape: T here is always a scalar-or-list (StringOrList or
// string) — never itself a map — so a MappingNode can only mean the scoped form;
// anything else is the plain default. Note: only the DEFAULT of a Scoped[StringOrList]
// may be a tiered list; a named surface takes a single value (one endpoint, one value),
// enforced in validation rather than the type.
type Scoped[T any] struct {
	Default   T
	BySurface map[string]T
}

func (s *Scoped[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		var raw map[string]T
		if err := n.Decode(&raw); err != nil {
			return err
		}
		def, ok := raw["default"]
		if !ok {
			return fmt.Errorf("scoped value must have a `default` (the fallback for un-named surfaces)")
		}
		delete(raw, "default")
		s.Default = def
		if len(raw) > 0 {
			s.BySurface = raw
		}
		return nil
	}
	return n.Decode(&s.Default)
}

// For returns the value scoped to surface, or the default when the surface is un-named.
func (s Scoped[T]) For(surface string) T {
	if s.BySurface != nil {
		if v, ok := s.BySurface[surface]; ok {
			return v
		}
	}
	return s.Default
}
