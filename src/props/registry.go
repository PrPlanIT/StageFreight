package props

import (
	"fmt"
	"sort"
)

var definitions []Definition
var definitionIndex = map[string]int{}

// Register adds a prop definition to the global registry.
// Panics on duplicate ID (caught at init time).
func Register(def Definition) {
	if _, exists := definitionIndex[def.ID]; exists {
		panic(fmt.Sprintf("props: duplicate definition ID %q", def.ID))
	}
	definitionIndex[def.ID] = len(definitions)
	definitions = append(definitions, def)
}

// Get returns a definition by type ID, or false if not found.
func Get(id string) (Definition, bool) {
	idx, ok := definitionIndex[id]
	if !ok {
		return Definition{}, false
	}
	return definitions[idx], true
}

// All returns all registered definitions in registration order.
func All() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

// List returns definitions filtered by category. Empty category returns all.
func List(category string) []Definition {
	if category == "" {
		return All()
	}
	var out []Definition
	for _, d := range definitions {
		if d.Category == category {
			out = append(out, d)
		}
	}
	return out
}

// CategoryCount holds a category name and the number of types in it.
type CategoryCount struct {
	Name  string
	Count int
}

// Categories returns all categories with their type counts, sorted by name.
func Categories() []CategoryCount {
	counts := map[string]int{}
	for _, d := range definitions {
		counts[d.Category]++
	}
	out := make([]CategoryCount, 0, len(counts))
	for name, count := range counts {
		out = append(out, CategoryCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}
