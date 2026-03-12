package commit

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TypeRegistry validates and resolves commit type keys.
type TypeRegistry struct {
	types map[string]config.CommitType
	order []config.CommitType
}

// NewTypeRegistry creates a registry from configured commit types.
func NewTypeRegistry(types []config.CommitType) *TypeRegistry {
	m := make(map[string]config.CommitType, len(types))
	for _, t := range types {
		m[t.Key] = t
	}
	return &TypeRegistry{types: m, order: types}
}

// Resolve returns the resolved type key and whether a bang (!) is forced.
// If the type has an AliasFor, it returns the alias target key.
func (r *TypeRegistry) Resolve(key string) (resolvedKey string, forceBang bool, err error) {
	t, ok := r.types[key]
	if !ok {
		return "", false, fmt.Errorf("unknown commit type %q (valid: %s)", key, r.validKeys())
	}
	if t.AliasFor != "" {
		return t.AliasFor, t.ForceBang, nil
	}
	return t.Key, t.ForceBang, nil
}

// Valid returns true if the key is a recognized commit type.
func (r *TypeRegistry) Valid(key string) bool {
	_, ok := r.types[key]
	return ok
}

// List returns all configured commit types in definition order.
func (r *TypeRegistry) List() []config.CommitType {
	return r.order
}

func (r *TypeRegistry) validKeys() string {
	keys := make([]string, 0, len(r.order))
	for _, t := range r.order {
		keys = append(keys, t.Key)
	}
	return strings.Join(keys, ", ")
}
