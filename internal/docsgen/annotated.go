package docsgen

import (
	"reflect"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

var idMapMarkerType = reflect.TypeOf((*config.IDMap)(nil)).Elem()

// isOrderedIDMap reports whether a field type is one of the id→entry map types:
// a Go slice that DECODES as a YAML map keyed by entry id (the house convention
// behind forges/repos/registries/builds/publish/stencils/…). Detection is the
// compile-checked config.IDMap marker, so a new map type joins docs correctly by
// implementing it — and never by naming convention. Their docs must render map
// form: the "- id:" list form these slices would otherwise render is a shape
// the strict decoder REJECTS.
func isOrderedIDMap(t reflect.Type) bool {
	t = unwrapPtr(t)
	return t.Kind() == reflect.Slice && t.Implements(idMapMarkerType)
}

// idMapEntryLine opens every id-map block: the entry key stands in for the id.
func idMapEntryLine(indent string) string { return indent + "<id>:   # entry key = the unique id" }

// idMapSkipsField reports whether a field is the entry's stamped ID — the map
// key, which must not render as a field inside the entry body.
func idMapSkipsField(f reflect.StructField) bool { return f.Name == "ID" }

// This file renders goreleaser-style annotated YAML: for a discriminated-union config
// section (targets, builds), it emits one YAML skeleton per kind, showing only that
// kind's fields with their description, allowed values, and required-ness as inline
// comments. The field metadata (comments, enums) comes from the same authoritative
// sources as the tables, so the blocks can't drift.

// kindBlock describes a union section that renders as per-kind YAML blocks instead of a
// single flattened table.
type kindBlock struct {
	typ   reflect.Type
	kinds []kindSpec // ordered, so the docs list kinds in a deliberate order
}

type kindSpec struct {
	name   string   // the kind value (e.g. "registry")
	fields []string // top-level yaml keys shown for this kind, in display order
}

// unionByType maps a discriminated-union struct's Go type name to its per-kind field
// lists, so the union renders as one annotated YAML block per kind — whether it appears at
// the top level (targets, builds) or nested inside another section (narrate items). Field
// lists are curated from each struct's "── kind: X ──" grouping and the validation rules.
var unionByType = map[string]kindBlock{
	"TargetConfig": {
		typ: reflect.TypeOf(config.TargetConfig{}),
		kinds: []kindSpec{
			{"registry", []string{"id", "kind", "registry", "build", "tags", "signing_profile", "native_scan", "retention", "when"}},
			{"metadata", []string{"id", "kind", "registry", "repos", "description", "readme", "website", "topics", "logo", "when"}},
			{"gitlab-component", []string{"id", "kind", "spec_files", "catalog", "when"}},
			{"release", []string{"id", "kind", "aliases", "tag", "archives", "prerelease", "mirror", "sync_release", "sync_assets", "signing_profile", "retention", "when"}},
			{"binary-archive", []string{"id", "kind", "build", "name", "format", "binary_name", "include", "checksums", "when"}},
			{"generic-package", []string{"id", "kind", "repo", "package", "version", "archives", "when"}},
			{"pages", []string{"id", "kind", "provider", "build", "dir", "domain", "project", "base_path", "exclude", "when"}},
		},
	},
	"BuildConfig": {
		typ: reflect.TypeOf(config.BuildConfig{}),
		kinds: []kindSpec{
			{"docker", []string{"id", "kind", "dockerfile", "context", "target", "platforms", "build_args"}},
			{"binary", []string{"id", "kind", "builder", "from", "output", "args", "env", "platforms"}},
			{"command", []string{"id", "kind", "image", "command", "env", "stage", "outputs"}},
		},
	},
	"StencilDef": {
		typ: reflect.TypeOf(config.StencilDef{}),
		kinds: []kindSpec{
			{"badge", []string{"type", "render", "label", "message", "color", "font", "font_size", "output", "link", "logo", "logo_color", "label_color"}},
			{"shield", []string{"type", "render", "shield", "link"}},
			{"text", []string{"type", "content"}},
			{"component", []string{"type", "spec"}},
			{"include", []string{"type", "path"}},
			{"contents", []string{"type", "build", "source", "section", "renderer", "columns", "output_file", "wrap", "summary", "style", "params"}},
		},
	},
}

func unionFor(t reflect.Type) (kindBlock, bool) {
	kb, ok := unionByType[t.Name()]
	return kb, ok
}

type nestedUnion struct {
	label string
	kb    kindBlock
}

// collectNestedUnions finds every discriminated-union field reachable from t at any depth
// (e.g. narrate → patches → items), returning each one's dotted label and union spec so the
// renderer can break it out into per-kind blocks.
func collectNestedUnions(t reflect.Type, prefix string, seen map[reflect.Type]bool) []nestedUnion {
	if seen[t] {
		return nil
	}
	seen[t] = true
	var out []nestedUnion
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("yaml") == ",inline" {
			if et := unwrapType(f.Type); isFirstPartyConfig(et) {
				out = append(out, collectNestedUnions(et, prefix, seen)...)
			}
			continue
		}
		yk := yamlKeyFromTag(f.Tag.Get("yaml"))
		if yk == "" || yk == "-" {
			continue
		}
		et := unwrapType(f.Type)
		if kb, ok := unionFor(et); ok {
			out = append(out, nestedUnion{label: strings.TrimSpace(prefix + yk), kb: kb})
		} else if isFirstPartyConfig(et) {
			out = append(out, collectNestedUnions(et, prefix+yk+" ", seen)...)
		}
	}
	return out
}

// kindFieldEnums are allowed-value sets that depend on the kind (the flat enumSources map
// can't express these because the field is shared across kinds with different meanings).
var kindFieldEnums = map[string][]string{
	"pages.provider": {"cloudflare", "github"},
}

// renderUnionBlocks emits one annotated YAML block per kind. topLevel wraps each block in
// the section's shape — id→entry map form when the section is an Ordered id-map
// (`builds:\n  <id>:\n    ...`), list form otherwise (`targets:\n  - ...`);
// nested renders a bare list item (`- ...`) under a "<section> <field> · kind: X"
// heading, since it's an entry in a parent list.
func renderUnionBlocks(kb kindBlock, topLevel bool, wrapperKey, label string, idMap bool) string {
	byKey := fieldsByYAMLKey(kb.typ)
	fieldIndent := "  "
	if topLevel {
		fieldIndent = "    "
	}
	var b strings.Builder
	for _, ks := range kb.kinds {
		var lines []string
		for _, key := range ks.fields {
			f, ok := byKey[key]
			if !ok || (idMap && idMapSkipsField(f)) {
				continue
			}
			lines = append(lines, yamlFieldLines(f, kb.typ.Name(), fieldIndent, wrapperKey, ks.name)...)
		}
		if len(lines) == 0 {
			continue
		}
		switch {
		case topLevel && idMap:
			lines = append([]string{idMapEntryLine("  ")}, lines...)
			b.WriteString("#### `kind: " + ks.name + "`\n\n")
			b.WriteString("```yaml\n" + wrapperKey + ":\n" + strings.Join(lines, "\n") + "\n```\n\n")
		case topLevel:
			lines[0] = "  - " + strings.TrimLeft(lines[0], " ")
			b.WriteString("#### `kind: " + ks.name + "`\n\n")
			b.WriteString("```yaml\n" + wrapperKey + ":\n" + strings.Join(lines, "\n") + "\n```\n\n")
		default:
			lines[0] = "- " + strings.TrimLeft(lines[0], " ")
			b.WriteString("#### " + label + " · `kind: " + ks.name + "`\n\n")
			b.WriteString("```yaml\n" + strings.Join(lines, "\n") + "\n```\n\n")
		}
	}
	return b.String()
}

// renderSectionYAML emits a single nested annotated YAML block for a first-party struct
// section (a list of the struct if isList, else a plain nested mapping).
func renderSectionYAML(sectionKey string, t reflect.Type, isList, isIDMap bool) string {
	indent := "  "
	if isList || isIDMap {
		indent = "    " // room under the list marker / the <id>: entry key
	}
	var lines []string
	for i := 0; i < t.NumField(); i++ {
		if isIDMap && idMapSkipsField(t.Field(i)) {
			continue
		}
		lines = append(lines, yamlFieldLines(t.Field(i), t.Name(), indent, sectionKey, "")...)
	}
	if len(lines) == 0 {
		return ""
	}
	if isIDMap {
		lines = append([]string{idMapEntryLine("  ")}, lines...)
	} else if isList {
		lines[0] = "  - " + strings.TrimLeft(lines[0], " ")
	}
	return "```yaml\n" + sectionKey + ":\n" + strings.Join(lines, "\n") + "\n```\n\n"
}

// yamlFieldLines emits the annotated YAML line(s) for one struct field. Inline-embedded
// structs are flattened in place; nested first-party structs recurse into a nested block;
// everything else is a single `key: <placeholder>` line with a trailing comment.
func yamlFieldLines(field reflect.StructField, declType, indent, docPrefix, kind string) []string {
	tag := field.Tag.Get("yaml")
	if tag == ",inline" {
		var lines []string
		if et := unwrapPtr(field.Type); et.Kind() == reflect.Struct {
			for i := 0; i < et.NumField(); i++ {
				lines = append(lines, yamlFieldLines(et.Field(i), et.Name(), indent, docPrefix, kind)...)
			}
		}
		return lines
	}
	yamlKey := yamlKeyFromTag(tag)
	if yamlKey == "" || yamlKey == "-" {
		return nil
	}
	docPath := docPrefix + "." + yamlKey

	// Allowed values: kind-specific override first, then the flat enum source.
	enum := kindFieldEnums[kind+"."+yamlKey]
	if len(enum) == 0 {
		enum = enumValuesFor(docPath)
	}
	comment := yamlComment(declType, field, enum)

	elem := unwrapType(field.Type)

	// A discriminated-union field isn't flattened — its shape depends on kind, so point to
	// the per-kind blocks rendered separately.
	if _, ok := unionFor(elem); ok {
		line := indent + yamlKey + ": []   # discriminated union by kind — see per-kind blocks below"
		return []string{line}
	}

	if isFirstPartyConfig(elem) {
		idMap := isOrderedIDMap(field.Type)
		list := !idMap && unwrapPtr(field.Type).Kind() == reflect.Slice
		childIndent := indent + "  "
		if list || idMap {
			childIndent = indent + "    " // room under the list marker / the <id>: entry key
		}
		var children []string
		for i := 0; i < elem.NumField(); i++ {
			if idMap && idMapSkipsField(elem.Field(i)) {
				continue
			}
			children = append(children, yamlFieldLines(elem.Field(i), elem.Name(), childIndent, docPath, "")...)
		}
		head := indent + yamlKey + ":"
		if len(children) == 0 {
			head += " {}" // no documentable children — avoid a dangling "key:"
		}
		if comment != "" {
			head += "   # " + comment
		}
		if idMap && len(children) > 0 {
			children = append([]string{idMapEntryLine(indent + "  ")}, children...)
		} else if list && len(children) > 0 {
			children[0] = indent + "  - " + strings.TrimLeft(children[0], " ")
		}
		return append([]string{head}, children...)
	}

	line := indent + yamlKey + ": " + yamlPlaceholder(field, yamlKey, kind)
	if comment != "" {
		line += "   # " + comment
	}
	return []string{line}
}

// yamlComment builds the inline comment: first sentence of the doc comment, then allowed
// values, then a required marker.
func yamlComment(declType string, field reflect.StructField, enum []string) string {
	var parts []string
	if d := conciseComment(configFieldComments[declType+"."+field.Name]); d != "" {
		parts = append(parts, d)
	}
	if len(enum) > 0 {
		parts = append(parts, "one of: "+strings.Join(enum, ", "))
	}
	if !strings.Contains(field.Tag.Get("yaml"), "omitempty") {
		parts = append(parts, "required")
	}
	return strings.Join(parts, " · ")
}

// yamlPlaceholder returns a type-appropriate placeholder value. The `kind` field shows the
// concrete kind rather than a placeholder.
func yamlPlaceholder(field reflect.StructField, yamlKey, kindVal string) string {
	if yamlKey == "kind" && kindVal != "" {
		return kindVal
	}
	t := unwrapPtr(field.Type)
	switch t.Kind() {
	case reflect.String:
		return "<string>"
	case reflect.Bool:
		return "false"
	case reflect.Int, reflect.Int32, reflect.Int64:
		return "<int>"
	case reflect.Slice:
		if unwrapPtr(t.Elem()).Kind() == reflect.String {
			return "[<string>]"
		}
		return "[...]"
	case reflect.Map:
		return "{}"
	default:
		return "<value>"
	}
}

// fieldsByYAMLKey indexes a struct's fields by their yaml key.
func fieldsByYAMLKey(t reflect.Type) map[string]reflect.StructField {
	out := make(map[string]reflect.StructField, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if k := yamlKeyFromTag(f.Tag.Get("yaml")); k != "" && k != "-" {
			out[k] = f
		}
	}
	return out
}

func unwrapPtr(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// unwrapType dereferences pointers and unwraps a slice to its element type, so a
// []*SubStruct field resolves to SubStruct.
func unwrapType(t reflect.Type) reflect.Type {
	t = unwrapPtr(t)
	if t.Kind() == reflect.Slice {
		t = unwrapPtr(t.Elem())
	}
	return t
}

// conciseComment caps a comment so inline YAML lines stay readable, truncating at a word
// boundary. It avoids sentence-splitting (which mangles "e.g." / "i.e.").
func conciseComment(s string) string {
	const max = 100
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > 40 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}
