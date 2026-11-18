package governance

import (
	"reflect"
	"testing"
)

// TestMergeConfigOverride: a repo's config merges onto its profile's like local keys onto
// a preset — maps by key, scalars and lists replaced, null removing the inherited key.
func TestMergeConfigOverride(t *testing.T) {
	base := map[string]any{
		"upstream":  map[string]any{"version": map[string]any{"track": "upstream", "ancestry": true}},
		"lint":      map[string]any{"preset": "preset/lint.yml", "exclude": []any{"a"}},
		"schedules": map[string]any{"preset": "preset/schedules.yml"},
		"publish":   map[string]any{"preset": "preset/publish.yml"},
	}
	mergeConfigOverride(base, map[string]any{
		"upstream":  map[string]any{"version": map[string]any{"since": "v1.0.0", "ancestry": nil}},
		"lint":      map[string]any{"exclude": []any{"b"}},
		"schedules": nil,
		"publish":   "replaced",
	})
	want := map[string]any{
		"upstream": map[string]any{"version": map[string]any{"track": "upstream", "since": "v1.0.0"}},
		"lint":     map[string]any{"preset": "preset/lint.yml", "exclude": []any{"b"}},
		"publish":  "replaced",
	}
	if !reflect.DeepEqual(base, want) {
		t.Errorf("merged = %#v\nwant   %#v", base, want)
	}
}
