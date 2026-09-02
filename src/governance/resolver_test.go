package governance

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeLoader is an in-memory preset loader for testing.
type fakeLoader map[string]string

func (f fakeLoader) Load(path string) ([]byte, error) {
	v, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("not found: %s", path)
	}
	return []byte(v), nil
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func requireEqual(t *testing.T, expected, actual any, msg string) {
	t.Helper()
	if fmt.Sprintf("%v", expected) != fmt.Sprintf("%v", actual) {
		t.Fatalf("%s: expected %v, got %v", msg, expected, actual)
	}
}

func requireTrue(t *testing.T, val bool, msg string) {
	t.Helper()
	if !val {
		t.Fatal(msg)
	}
}

// toNode marshals a map fixture to YAML and back to a *yaml.Node, so semantic tests
// keep readable map literals while exercising the node-based resolver. Key order is
// not meaningful for these tests; the dedicated order test uses parseNode.
func toNode(t *testing.T, m map[string]any) *yaml.Node {
	t.Helper()
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return &n
}

// parseNode parses YAML text into a *yaml.Node, preserving document order.
func parseNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &n
}

// decodeMap decodes a resolved node into a map for value assertions.
func decodeMap(t *testing.T, n *yaml.Node) map[string]any {
	t.Helper()
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// nodeKeys returns the ordered keys of the mapping at a dotted path within a node.
func nodeKeys(t *testing.T, n *yaml.Node, path string) []string {
	t.Helper()
	cur := n
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}
	for _, seg := range strings.Split(path, ".") {
		found := false
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if cur.Content[i].Value == seg {
				cur = cur.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("nodeKeys: path %q: segment %q not found", path, seg)
		}
	}
	var ks []string
	for i := 0; i+1 < len(cur.Content); i += 2 {
		ks = append(ks, cur.Content[i].Value)
	}
	return ks
}

// --- Resolver Tests ---

func TestResolvePresets_SimplePreset(t *testing.T) {
	loader := fakeLoader{
		"preset/security.yml": `
security:
  enabled: true
  sbom: true
`,
	}

	raw := map[string]any{
		"security": map[string]any{
			"preset": "preset/security.yml",
		},
	}

	resolved, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	sec := decodeMap(t, resolved)["security"].(map[string]any)
	requireEqual(t, true, sec["enabled"], "security.enabled")
	requireEqual(t, true, sec["sbom"], "security.sbom")
}

func TestResolvePresets_ScalarOverride(t *testing.T) {
	loader := fakeLoader{
		"preset/security.yml": `
security:
  enabled: true
  sbom: true
`,
	}

	raw := map[string]any{
		"security": map[string]any{
			"preset": "preset/security.yml",
			"sbom":   false,
		},
	}

	resolved, entries, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	sec := decodeMap(t, resolved)["security"].(map[string]any)
	requireEqual(t, false, sec["sbom"], "local override should win")

	// Check that the preset entry for sbom was marked as overridden.
	foundOverride := false
	for _, e := range entries {
		if e.Path == "security.sbom" && e.Overridden {
			foundOverride = true
		}
	}
	requireTrue(t, foundOverride, "expected override trace for security.sbom")
}

func TestResolvePresets_ListReplacement(t *testing.T) {
	loader := fakeLoader{
		"preset/targets.yml": `
targets:
  items:
    - id: base
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"preset": "preset/targets.yml",
			"items":  []any{"override"},
		},
	}

	_, entries, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	foundReplace := false
	for _, e := range entries {
		if e.Path == "targets.items" && e.Operation == "replace" {
			foundReplace = true
		}
	}
	requireTrue(t, foundReplace, "expected list replacement to be recorded as 'replace'")
}

func TestResolvePresets_CycleDetection(t *testing.T) {
	loader := fakeLoader{
		"a.yml": `
targets:
  preset: "b.yml"
`,
		"b.yml": `
targets:
  preset: "a.yml"
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"preset": "a.yml",
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
}

func TestResolvePresets_SingleKeyValidation(t *testing.T) {
	loader := fakeLoader{
		"bad.yml": `
targets:
  items: []
badges:
  items: []
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"preset": "bad.yml",
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err) // must reject multi-key presets
}

func TestResolvePresets_KeyMismatch(t *testing.T) {
	loader := fakeLoader{
		"wrong.yml": `
badges:
  items: []
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"preset": "wrong.yml",
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err) // preset declares "badges" but imported into "targets"
}

func TestResolvePresets_SourceRefNotConcatenated(t *testing.T) {
	loader := fakeLoader{
		"preset/security.yml": `
security:
  enabled: true
`,
	}

	raw := map[string]any{
		"security": map[string]any{
			"preset": "preset/security.yml",
		},
	}

	_, entries, err := ResolvePresets(toNode(t, raw), loader, "PrPlanIT/MaintenancePolicy@v1.0.0", "inline", 0, nil)
	requireNoError(t, err)

	for _, e := range entries {
		if e.Source == "preset:preset/security.yml" {
			requireEqual(t, "PrPlanIT/MaintenancePolicy@v1.0.0", e.SourceRef,
				"sourceRef should be repo identity, not concatenated path")
		}
	}
}

// --- presets: [...] ordered composition tests ---

func TestResolvePresetList_OrderedComposition(t *testing.T) {
	loader := fakeLoader{
		"preset/a.yml": `
targets:
  - id: alpha
    kind: registry
`,
		"preset/b.yml": `
targets:
  - id: beta
    kind: registry
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"presets": []any{"preset/a.yml", "preset/b.yml"},
		},
	}

	resolved, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	items := decodeMap(t, resolved)["targets"].([]any)
	requireEqual(t, 2, len(items), "expected 2 targets")
	requireEqual(t, "alpha", items[0].(map[string]any)["id"], "first item should be alpha")
	requireEqual(t, "beta", items[1].(map[string]any)["id"], "second item should be beta")
}

func TestResolvePresetList_DuplicateIDHardFails(t *testing.T) {
	loader := fakeLoader{
		"preset/a.yml": `
targets:
  - id: shared-release
    kind: release
`,
		"preset/b.yml": `
targets:
  - id: shared-release
    kind: release
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"presets": []any{"preset/a.yml", "preset/b.yml"},
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
	requireTrue(t, indexOf(err.Error(), "preset/a.yml") >= 0, "error should name first contributing preset")
	requireTrue(t, indexOf(err.Error(), "preset/b.yml") >= 0, "error should name duplicate preset")
}

func TestResolvePresetList_LocalSiblingsAppend(t *testing.T) {
	loader := fakeLoader{
		"preset/targets-base.yml": `
targets:
  - id: preset-target
    kind: registry
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"presets": []any{"preset/targets-base.yml"},
			"items": []any{
				map[string]any{"id": "inline-target", "kind": "binary-archive"},
			},
		},
	}

	resolved, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	items := decodeMap(t, resolved)["targets"].([]any)
	requireEqual(t, 2, len(items), "expected preset item + inline item")
	requireEqual(t, "preset-target", items[0].(map[string]any)["id"], "preset item first")
	requireEqual(t, "inline-target", items[1].(map[string]any)["id"], "inline item appended after presets")
}

func TestResolvePresetList_MissingNavPathHardFails(t *testing.T) {
	loader := fakeLoader{
		"preset/invalid.yml": `
badges:
  wrong_key:
    - id: build
`,
	}

	raw := map[string]any{
		"badges": map[string]any{
			"items": map[string]any{
				"presets": []any{"preset/invalid.yml"},
			},
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
	requireTrue(t, indexOf(err.Error(), "items") >= 0, "error should mention the missing navigation key")
}

func TestResolvePresetList_PresetAndPresetsBothSet(t *testing.T) {
	loader := fakeLoader{}

	raw := map[string]any{
		"targets": map[string]any{
			"preset":  "preset/a.yml",
			"presets": []any{"preset/b.yml"},
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
}

func TestResolvePresetList_PresetsOnNonKeyedSection(t *testing.T) {
	loader := fakeLoader{}

	raw := map[string]any{
		"security": map[string]any{
			"presets": []any{"preset/security.yml"},
		},
	}

	_, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
}

func TestResolvePresetList_EmptyList(t *testing.T) {
	loader := fakeLoader{}

	raw := map[string]any{
		"targets": map[string]any{
			"presets": []any{},
		},
	}

	resolved, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	items, _ := decodeMap(t, resolved)["targets"].([]any)
	requireEqual(t, 0, len(items), "empty presets should produce empty list")
}

func TestResolvePresetList_BackCompatSinglePreset(t *testing.T) {
	loader := fakeLoader{
		"preset/security.yml": `
security:
  enabled: true
  sbom: true
`,
	}

	raw := map[string]any{
		"security": map[string]any{
			"preset": "preset/security.yml",
		},
	}

	resolved, _, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	sec := decodeMap(t, resolved)["security"].(map[string]any)
	requireEqual(t, true, sec["enabled"], "backward compat: preset: form still works for enabled")
	requireEqual(t, true, sec["sbom"], "backward compat: preset: form still works for sbom")
}

func TestResolvePresetList_ProvenanceTagging(t *testing.T) {
	loader := fakeLoader{
		"preset/targets-a.yml": `
targets:
  - id: alpha
    kind: registry
`,
		"preset/targets-b.yml": `
targets:
  - id: beta
    kind: registry
`,
	}

	raw := map[string]any{
		"targets": map[string]any{
			"presets": []any{"preset/targets-a.yml", "preset/targets-b.yml"},
		},
	}

	_, entries, err := ResolvePresets(toNode(t, raw), loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	alphaSource := ""
	betaSource := ""
	for _, e := range entries {
		if indexOf(e.Path, "alpha") >= 0 {
			alphaSource = e.Source
		}
		if indexOf(e.Path, "beta") >= 0 {
			betaSource = e.Source
		}
	}
	requireEqual(t, "preset:preset/targets-a.yml", alphaSource, "alpha should be tagged with targets-a.yml")
	requireEqual(t, "preset:preset/targets-b.yml", betaSource, "beta should be tagged with targets-b.yml")
}

// --- scribe keyed-MAP preset composition (order-preserving) ---

// TestResolvePresetMap_ScribeContentComposesInOrder proves the whole point of the
// node-based resolver: scribe.content is a keyed MAP, and composing it from a preset +
// inline entries keeps DOCUMENT ORDER (preset entries first, then inline) — a
// map[string]any round-trip would alphabetize it.
func TestResolvePresetMap_ScribeContentComposesInOrder(t *testing.T) {
	loader := fakeLoader{
		"preset/badges.yml": "stencils:\n  build: { label: build }\n  license: { label: license }\n",
	}
	raw := parseNode(t, "stencils:\n  presets: [preset/badges.yml]\n  release: { label: release }\n")

	resolved, _, err := ResolvePresets(raw, loader, "test@v1", "inline", 0, nil)
	requireNoError(t, err)

	got := nodeKeys(t, resolved, "stencils")
	requireEqual(t, "[build license release]", fmt.Sprintf("%v", got),
		"stencils must compose in document order (preset entries, then inline)")
}

// TestResolvePresetMap_ScribeContentDuplicateKeyFails covers the keyed-map dedup: two
// presets contributing the same content id is a hard error (the map analogue of the
// list duplicate-id guard).
func TestResolvePresetMap_ScribeContentDuplicateKeyFails(t *testing.T) {
	loader := fakeLoader{
		"preset/a.yml": "stencils:\n  build: { label: a }\n",
		"preset/b.yml": "stencils:\n  build: { label: b }\n",
	}
	raw := parseNode(t, "stencils:\n  presets: [preset/a.yml, preset/b.yml]\n")

	_, _, err := ResolvePresets(raw, loader, "test@v1", "inline", 0, nil)
	requireError(t, err)
	requireTrue(t, indexOf(err.Error(), "build") >= 0, "error should name the duplicate content key")
}

// --- Renderer Tests ---

func TestRenderSealedConfig_Deterministic(t *testing.T) {
	config := map[string]any{
		"security": map[string]any{"sbom": true},
		"targets":  []any{"a"},
	}

	seal := SealMeta{
		SourceRepo: "test",
		ProfileID:  "test-cluster",
	}

	a, err := RenderSealedConfig(seal, config)
	requireNoError(t, err)

	b, err := RenderSealedConfig(seal, config)
	requireNoError(t, err)

	requireEqual(t, string(a), string(b), "render must be deterministic")
}

func TestRenderSealedConfig_HasSeal(t *testing.T) {
	config := map[string]any{
		"version": 1,
	}

	seal := SealMeta{
		SourceRepo: "https://gitlab.example.com/example-org/policy-repo",
		ProfileID:  "docker-services",
	}

	out, err := RenderSealedConfig(seal, config)
	requireNoError(t, err)

	s := string(out)
	requireTrue(t, indexOf(s, "GENERATED / ENFORCED BY STAGEFREIGHT GOVERNANCE") >= 0, "seal header missing")
	requireTrue(t, indexOf(s, "policy-repo") >= 0, "source repo missing from seal")
	requireTrue(t, indexOf(s, "v1.0.0") < 0, "seal must not record a revision — it changes every push and rewrites every satellite")
	requireTrue(t, indexOf(s, "docker-services") >= 0, "cluster ID missing from seal")
}

func TestRenderSealedConfig_CanonicalOrder(t *testing.T) {
	config := map[string]any{
		"release":  map[string]any{"enabled": true},
		"security": map[string]any{"sbom": true},
		"targets":  []any{"a"},
	}

	seal := SealMeta{SourceRepo: "test", ProfileID: "test"}

	out, err := RenderSealedConfig(seal, config)
	requireNoError(t, err)

	s := string(out)
	targetsIdx := indexOf(s, "targets:")
	securityIdx := indexOf(s, "security:")
	releaseIdx := indexOf(s, "release:")

	requireTrue(t, targetsIdx < securityIdx, "targets should come before security")
	requireTrue(t, securityIdx < releaseIdx, "security should come before release")
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
