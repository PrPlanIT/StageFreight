package registry

import "sort"

// KnownProviders returns the recognized registry provider names, sorted — for docs and
// tooling that need to list allowed values from the authoritative source. The empty
// "auto-detect" sentinel is omitted (it isn't a value a user types).
func KnownProviders() []string {
	out := make([]string, 0, len(knownProviders))
	for k := range knownProviders {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
