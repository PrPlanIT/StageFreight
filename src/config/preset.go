package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PresetLoader fetches preset content by path.
// Implementations: local filesystem, git repo checkout.
type PresetLoader interface {
	Load(path string) ([]byte, error)
}

// PolicyPresetLoader is an optional PresetLoader capability: resolve a reference under
// the mismatch policy declared beside it. A loader without it resolves under the
// default, which is to stop.
type PolicyPresetLoader interface {
	LoadWithPolicy(path, onMismatch string) ([]byte, error)
}

// loadPreset resolves a reference under the policy declared beside it, when the loader
// can honour one.
func loadPreset(loader PresetLoader, path, onMismatch string) ([]byte, error) {
	if onMismatch == "" {
		return loader.Load(path)
	}
	if pl, ok := loader.(PolicyPresetLoader); ok {
		return pl.LoadWithPolicy(path, onMismatch)
	}
	return loader.Load(path)
}

// extractMismatchPolicy returns the on_mismatch sibling of a preset reference, if any.
func extractMismatchPolicy(section *yaml.Node) string {
	v, ok := mapGet(section, "on_mismatch")
	if !ok || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}

// MergeTrace records how each config value was resolved.
type MergeTrace struct {
	Entries []MergeEntry
}

// MergeEntry records the provenance of a single config value.
type MergeEntry struct {
	Path         string // dot-path (e.g., "security.sbom")
	Source       string // "managed", "local", "preset:preset/security.yml"
	SourceRef    string // "PrPlanIT/MaintenancePolicy@v1.0.0" for presets
	Layer        int    // resolution depth (0=innermost preset, N=outermost, N+1=managed, N+2=local)
	Operation    string // "set", "override", "merge", "replace"
	Value        any
	Overridden   bool
	OverriddenBy string
}

// keyedListSections maps a section path to the item key field for ordered LIST
// composition (presets: [...] on a `[]` section, dedup by that field).
var keyedListSections = map[string]string{
	"targets":                  "id",
	"builds":                   "id",
	"badges.items":             "id",
	"versioning.tag_sources":   "id",
	"versioning.branch_builds": "id",
}

// keyedMapSections are order-preserving keyed-MAP sections (id → entry) that support
// presets: [...] composition by merging map entries (dedup by key). These decode via
// decodeIDMap and are document-order sensitive, which is exactly why the whole preset
// layer runs on yaml.Node — a map[string]any round-trip would alphabetize them.
var keyedMapSections = map[string]bool{
	"stencils":     true,
	"scribe.files": true,
}

// ── yaml.Node accessor kit ───────────────────────────────────────────────────
// Free helpers over the SAME *yaml.Node representation decodeIDMap consumes, so
// preset composition preserves order end-to-end (no map[string]any hop).

func isMapping(n *yaml.Node) bool  { return n != nil && n.Kind == yaml.MappingNode }
func isSequence(n *yaml.Node) bool { return n != nil && n.Kind == yaml.SequenceNode }

// docRoot unwraps a DocumentNode to its root content node.
func docRoot(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// mapGet returns the value node for key in mapping m, or nil,false.
func mapGet(m *yaml.Node, key string) (*yaml.Node, bool) {
	if !isMapping(m) {
		return nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], true
		}
	}
	return nil, false
}

// mapSet replaces key's value in place (preserving position) or appends it.
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

// mapKeys returns mapping m's keys in document order.
func mapKeys(m *yaml.Node) []string {
	if !isMapping(m) {
		return nil
	}
	keys := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

func newMapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func newScalar(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// nodeToAny decodes a node into a plain Go value for provenance display.
func nodeToAny(n *yaml.Node) any {
	var v any
	_ = n.Decode(&v)
	return v
}

// cloneShallow copies a mapping node's pair slice so mapSet on the copy does not
// mutate the original's ordering (values are shared — safe, treated read-only).
func cloneShallow(m *yaml.Node) *yaml.Node {
	c := &yaml.Node{Kind: yaml.MappingNode, Tag: m.Tag}
	c.Content = append(c.Content, m.Content...)
	return c
}

// ── engine ───────────────────────────────────────────────────────────────────

// ResolvePresets walks a config node, finds all preset:/presets: references, loads the
// preset files, validates the single-key invariant, and merges — order-preserving.
// Recursive: presets may reference other presets (depth-first). sourceRef is the repo
// identity; sourcePath is the current file. Returns the resolved node + provenance.
func ResolvePresets(root *yaml.Node, loader PresetLoader, sourceRef, sourcePath string, depth int, seen map[string]bool) (*yaml.Node, []MergeEntry, error) {
	if seen == nil {
		seen = make(map[string]bool)
	}
	return resolvePresetsInner(docRoot(root), loader, sourceRef, sourcePath, depth, seen, "")
}

func resolvePresetsInner(raw *yaml.Node, loader PresetLoader, sourceRef, sourcePath string, depth int, seen map[string]bool, pathPrefix string) (*yaml.Node, []MergeEntry, error) {
	if !isMapping(raw) {
		return raw, nil, nil
	}

	var entries []MergeEntry
	result := newMapping()

	for _, key := range mapKeys(raw) {
		val, _ := mapGet(raw, key)
		currentPath := key
		if pathPrefix != "" {
			currentPath = pathPrefix + "." + key
		}

		// The governance section's payload (profiles[].config) is OPAQUE at load: its
		// nested preset: refs resolve at DISTRIBUTION, per-satellite, with per-repo vars
		// — not at the control repo's own load. Copy the whole subtree verbatim rather
		// than recursing into it (which would prematurely resolve the satellite presets,
		// leaving their per-repo {var:} unresolved here). (With the retired list-form
		// clusters:, recursion skipped the sequence; the id-map profiles: is a mapping,
		// so this explicit skip restores that opacity.)
		if currentPath == "governance" {
			mapSet(result, key, val)
			entries = append(entries, MergeEntry{
				Path: currentPath, Source: sourcePath, SourceRef: sourceRef,
				Layer: depth, Operation: "set", Value: nodeToAny(val),
			})
			continue
		}

		if !isMapping(val) {
			// Scalar or sequence — copy directly.
			mapSet(result, key, val)
			entries = append(entries, MergeEntry{
				Path: currentPath, Source: sourcePath, SourceRef: sourceRef,
				Layer: depth, Operation: "set", Value: nodeToAny(val),
			})
			continue
		}
		section := val

		presetPath, hasPreset := extractPresetPath(section)
		presetsList, hasPresets := extractPresetsList(section)
		if hasPreset && hasPresets {
			return nil, nil, fmt.Errorf("%s: cannot specify both preset: and presets:", currentPath)
		}

		// presets: [...] — ordered composition for keyed-collection sections.
		if hasPresets {
			if keyField, isListKeyed := keyedListSections[currentPath]; isListKeyed {
				listNode, listEntries, err := resolvePresetList(presetsList, currentPath, section, loader, sourceRef, sourcePath, depth, seen, keyField)
				if err != nil {
					return nil, nil, err
				}
				mapSet(result, key, listNode)
				entries = append(entries, listEntries...)
				continue
			}
			if keyedMapSections[currentPath] {
				mapNode, mapEntries, err := resolvePresetMap(presetsList, currentPath, section, loader, sourceRef, sourcePath, depth, seen)
				if err != nil {
					return nil, nil, err
				}
				mapSet(result, key, mapNode)
				entries = append(entries, mapEntries...)
				continue
			}
			return nil, nil, fmt.Errorf("%s: presets: is only allowed on keyed-collection sections (targets, builds, badges.items, versioning.tag_sources, versioning.branch_builds, stencils, scribe.files)", currentPath)
		}

		if !hasPreset {
			// No preset — recurse into subsections.
			resolved, subEntries, err := resolvePresetsInner(section, loader, sourceRef, sourcePath, depth, seen, currentPath)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", key, err)
			}
			mapSet(result, key, resolved)
			entries = append(entries, subEntries...)
			continue
		}

		// --- Single preset: "path" handling ---

		if seen[presetPath] {
			return nil, nil, fmt.Errorf("%s: circular preset reference: %s", key, presetPath)
		}
		seen[presetPath] = true

		presetContent, err := loadPreset(loader, presetPath, extractMismatchPolicy(section))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: loading preset %q: %w", key, presetPath, err)
		}
		topKey, presetValue, err := ValidatePreset(presetContent)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: preset %q: %w", key, presetPath, err)
		}
		if topKey != key {
			return nil, nil, fmt.Errorf("%s: preset %q declares top-level key %q, expected %q", key, presetPath, topKey, key)
		}

		// Preset value is a scalar/sequence (e.g. targets: [...]) — use directly.
		if !isMapping(presetValue) {
			localOverrides := withoutKey(withoutKey(section, "preset"), "on_mismatch")
			if len(mapKeys(localOverrides)) > 0 {
				mapSet(result, key, localOverrides)
			} else {
				mapSet(result, key, presetValue)
			}
			entries = append(entries, MergeEntry{
				Path: currentPath, Source: "preset:" + presetPath, SourceRef: sourceRef,
				Layer: depth + 1, Operation: "set", Value: nodeToAny(presetValue),
			})
			delete(seen, presetPath)
			continue
		}

		// Nested/chained preset inside the loaded preset.
		innerPresetPath, hasInnerPreset := extractPresetPath(presetValue)
		var resolvedPreset *yaml.Node
		var presetEntries []MergeEntry

		if hasInnerPreset {
			if seen[innerPresetPath] {
				return nil, nil, fmt.Errorf("%s: circular preset reference: %s → %s", key, presetPath, innerPresetPath)
			}
			innerInner := newMapping()
			mapSet(innerInner, "preset", newScalar(innerPresetPath))
			innerWrapped := newMapping()
			mapSet(innerWrapped, topKey, innerInner)
			resolvedInner, innerEntries, err := resolvePresetsInner(innerWrapped, loader, sourceRef, presetPath, depth+1, seen, "")
			if err != nil {
				return nil, nil, fmt.Errorf("%s: resolving nested preset %q in %q: %w", key, innerPresetPath, presetPath, err)
			}
			innerSection, _ := mapGet(resolvedInner, topKey)
			currentOverrides := withoutKey(withoutKey(presetValue, "preset"), "on_mismatch")
			resolvedPreset = DeepMerge(innerSection, currentOverrides)
			presetEntries = innerEntries
		} else {
			resolvedPreset, presetEntries, err = resolvePresetsInner(presetValue, loader, sourceRef, presetPath, depth+1, seen, currentPath)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: resolving preset %q: %w", key, presetPath, err)
			}
		}

		for i := range presetEntries {
			presetEntries[i].Source = "preset:" + presetPath
		}
		entries = append(entries, presetEntries...)

		// Local siblings (everything except preset:) override the preset.
		localOverrides := withoutKey(withoutKey(section, "preset"), "on_mismatch")
		merged := DeepMerge(resolvedPreset, localOverrides)

		for _, localKey := range mapKeys(localOverrides) {
			overriddenPath := currentPath + "." + localKey
			for i := range entries {
				if entries[i].Path == overriddenPath && !entries[i].Overridden {
					entries[i].Overridden = true
					entries[i].OverriddenBy = sourcePath
				}
			}
		}
		for _, localKey := range mapKeys(localOverrides) {
			lv, _ := mapGet(localOverrides, localKey)
			path := currentPath + "." + localKey
			op := "override"
			if isSequence(lv) {
				op = "replace"
			}
			entries = append(entries, MergeEntry{
				Path: path, Source: sourcePath, SourceRef: sourceRef,
				Layer: depth, Operation: op, Value: nodeToAny(lv),
			})
		}

		mapSet(result, key, merged)
		delete(seen, presetPath)
	}

	return result, entries, nil
}

// resolvePresetList composes an ordered SEQUENCE node from an ordered list of presets
// plus inline items:, deduping by keyField. Order is preset order, then inline.
func resolvePresetList(presets []string, sectionPath string, section *yaml.Node, loader PresetLoader, sourceRef, sourcePath string, depth int, seen map[string]bool, keyField string) (*yaml.Node, []MergeEntry, error) {
	topKey, navPath := splitSectionPath(sectionPath)

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	var entries []MergeEntry
	seenIDs := make(map[string]string)

	appendItem := func(item *yaml.Node, src string, layer int, inlineSuffix string) error {
		if !isMapping(item) {
			return fmt.Errorf("%s: %sitem is not a map", sectionPath, inlineSuffix)
		}
		idNode, hasID := mapGet(item, keyField)
		if !hasID {
			return fmt.Errorf("%s: %sitem missing %q field", sectionPath, inlineSuffix, keyField)
		}
		idStr := idNode.Value
		if firstPath, dup := seenIDs[idStr]; dup {
			return fmt.Errorf("%s: duplicate %s %q\n  first contributed by: %s\n  duplicate from:       %s%s",
				sectionPath, keyField, idStr, firstPath, src, inlineSuffix)
		}
		seenIDs[idStr] = src
		seq.Content = append(seq.Content, item)
		entries = append(entries, MergeEntry{
			Path: fmt.Sprintf("%s[%s]", sectionPath, idStr), Source: src, SourceRef: sourceRef,
			Layer: layer, Operation: "append", Value: nodeToAny(item),
		})
		return nil
	}

	for _, presetPath := range presets {
		listNode, err := loadNavigatedPreset(presetPath, topKey, navPath, sectionPath, loader, seen)
		if err != nil {
			return nil, nil, err
		}
		if !isSequence(listNode) {
			delete(seen, presetPath)
			continue
		}
		for _, item := range listNode.Content {
			if err := appendItem(item, "preset:"+presetPath, depth+1, ""); err != nil {
				return nil, nil, err
			}
		}
		delete(seen, presetPath)
	}

	if inlineNode, ok := mapGet(section, "items"); ok && isSequence(inlineNode) {
		for _, item := range inlineNode.Content {
			if err := appendItem(item, sourcePath, depth, " (inline) "); err != nil {
				return nil, nil, err
			}
		}
	}

	return seq, entries, nil
}

// resolvePresetMap composes an ordered MAPPING node (id → entry) from an ordered list
// of presets plus the section's own inline entries, deduping by key. This is the
// keyed-MAP analogue of resolvePresetList — scribe.content/files are maps, not lists,
// and stay maps (order-preserving) through composition.
func resolvePresetMap(presets []string, sectionPath string, section *yaml.Node, loader PresetLoader, sourceRef, sourcePath string, depth int, seen map[string]bool) (*yaml.Node, []MergeEntry, error) {
	topKey, navPath := splitSectionPath(sectionPath)

	out := newMapping()
	var entries []MergeEntry
	seenIDs := make(map[string]string)

	add := func(k string, v *yaml.Node, src string, layer int) error {
		if firstPath, dup := seenIDs[k]; dup {
			return fmt.Errorf("%s: duplicate key %q\n  first contributed by: %s\n  duplicate from:       %s",
				sectionPath, k, firstPath, src)
		}
		seenIDs[k] = src
		mapSet(out, k, v)
		entries = append(entries, MergeEntry{
			Path: fmt.Sprintf("%s.%s", sectionPath, k), Source: src, SourceRef: sourceRef,
			Layer: layer, Operation: "append", Value: nodeToAny(v),
		})
		return nil
	}

	for _, presetPath := range presets {
		mapNode, err := loadNavigatedPreset(presetPath, topKey, navPath, sectionPath, loader, seen)
		if err != nil {
			return nil, nil, err
		}
		if !isMapping(mapNode) {
			delete(seen, presetPath)
			continue
		}
		for _, k := range mapKeys(mapNode) {
			v, _ := mapGet(mapNode, k)
			if err := add(k, v, "preset:"+presetPath, depth+1); err != nil {
				return nil, nil, err
			}
		}
		delete(seen, presetPath)
	}

	// Inline entries: the section's own keys (except the preset directives).
	for _, k := range mapKeys(section) {
		if k == "preset" || k == "presets" || k == "on_mismatch" {
			continue
		}
		v, _ := mapGet(section, k)
		if err := add(k, v, sourcePath, depth); err != nil {
			return nil, nil, err
		}
	}

	return out, entries, nil
}

// loadNavigatedPreset loads a preset, validates its single top key matches topKey, and
// navigates navPath to the composed value node (a sequence or mapping).
func loadNavigatedPreset(presetPath, topKey string, navPath []string, sectionPath string, loader PresetLoader, seen map[string]bool) (*yaml.Node, error) {
	if seen[presetPath] {
		return nil, fmt.Errorf("%s: circular preset reference: %s", sectionPath, presetPath)
	}
	seen[presetPath] = true

	content, err := loader.Load(presetPath)
	if err != nil {
		return nil, fmt.Errorf("%s: loading preset %q: %w", sectionPath, presetPath, err)
	}
	loadedTopKey, val, err := ValidatePreset(content)
	if err != nil {
		return nil, fmt.Errorf("%s: preset %q: %w", sectionPath, presetPath, err)
	}
	if loadedTopKey != topKey {
		return nil, fmt.Errorf("%s: preset %q declares top-level key %q, expected %q", sectionPath, presetPath, loadedTopKey, topKey)
	}
	for _, nav := range navPath {
		if !isMapping(val) {
			return nil, fmt.Errorf("%s: preset %q: expected map while navigating to %q", sectionPath, presetPath, nav)
		}
		next, exists := mapGet(val, nav)
		if !exists {
			return nil, fmt.Errorf("%s: preset %q: missing key %q in navigation path", sectionPath, presetPath, nav)
		}
		val = next
	}
	return val, nil
}

func splitSectionPath(sectionPath string) (topKey string, navPath []string) {
	parts := strings.SplitN(sectionPath, ".", 2)
	topKey = parts[0]
	if len(parts) > 1 {
		navPath = strings.Split(parts[1], ".")
	}
	return topKey, navPath
}

// ValidatePreset parses a preset and enforces exactly one top-level key, returning that
// key and its value node.
func ValidatePreset(content []byte) (string, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return "", nil, fmt.Errorf("invalid YAML: %w", err)
	}
	root := docRoot(&doc)
	keys := mapKeys(root)
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("preset is empty (no top-level keys)")
	}
	if len(keys) > 1 {
		return "", nil, fmt.Errorf("preset declares %d top-level keys (%s), must declare exactly one",
			len(keys), strings.Join(keys, ", "))
	}
	val, _ := mapGet(root, keys[0])
	return keys[0], val, nil
}

// extractPresetPath returns the scalar "preset" path in a mapping, if any.
func extractPresetPath(section *yaml.Node) (string, bool) {
	v, ok := mapGet(section, "preset")
	if !ok || v.Kind != yaml.ScalarNode || v.Value == "" {
		return "", false
	}
	return v.Value, true
}

// extractPresetsList returns the "presets" string list in a mapping, if any.
func extractPresetsList(section *yaml.Node) ([]string, bool) {
	v, ok := mapGet(section, "presets")
	if !ok || !isSequence(v) {
		return nil, false
	}
	paths := make([]string, 0, len(v.Content))
	for _, item := range v.Content {
		if item.Kind != yaml.ScalarNode || item.Value == "" {
			return nil, false
		}
		paths = append(paths, item.Value)
	}
	return paths, true
}

// withoutKey returns a copy of mapping m with key removed (order preserved).
func withoutKey(m *yaml.Node, key string) *yaml.Node {
	out := newMapping()
	for _, k := range mapKeys(m) {
		if k != key {
			v, _ := mapGet(m, k)
			mapSet(out, k, v)
		}
	}
	return out
}

// DeepMerge deep-merges override into base (both mapping nodes), override wins.
// Objects deep-merge (base order preserved, new keys appended); scalars/sequences
// replace. Non-mapping inputs: override replaces.
func DeepMerge(base, override *yaml.Node) *yaml.Node {
	if !isMapping(base) || !isMapping(override) {
		return override
	}
	result := cloneShallow(base)
	for _, k := range mapKeys(override) {
		ov, _ := mapGet(override, k)
		bv, exists := mapGet(result, k)
		if exists && isMapping(bv) && isMapping(ov) {
			mapSet(result, k, DeepMerge(bv, ov))
		} else {
			mapSet(result, k, ov)
		}
	}
	return result
}
