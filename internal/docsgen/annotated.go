package docsgen

import (
	"reflect"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

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

// kindBlocks are the union sections. Field lists are curated from each struct's
// "── kind: X ──" grouping and the per-kind validation rules.
var kindBlocks = map[string]kindBlock{
	"targets": {
		typ: reflect.TypeOf(config.TargetConfig{}),
		kinds: []kindSpec{
			{"registry", []string{"id", "kind", "registry", "build", "tags", "signing_profile", "native_scan", "retention", "when"}},
			{"docker-readme", []string{"id", "kind", "registry", "file", "link_base", "when"}},
			{"gitlab-component", []string{"id", "kind", "spec_files", "catalog", "when"}},
			{"release", []string{"id", "kind", "aliases", "tag", "archives", "prerelease", "mirror", "sync_release", "sync_assets", "signing_profile", "retention", "when"}},
			{"binary-archive", []string{"id", "kind", "build", "name", "format", "binary_name", "include", "checksums", "when"}},
			{"generic-package", []string{"id", "kind", "repo", "package", "version", "archives", "when"}},
			{"pages", []string{"id", "kind", "provider", "build", "dir", "domain", "project", "base_path", "exclude", "when"}},
		},
	},
	"builds": {
		typ: reflect.TypeOf(config.BuildConfig{}),
		kinds: []kindSpec{
			{"docker", []string{"id", "kind", "dockerfile", "context", "target", "platforms", "build_args"}},
			{"binary", []string{"id", "kind", "builder", "from", "output", "args", "env", "platforms"}},
			{"command", []string{"id", "kind", "image", "command", "env", "stage", "outputs"}},
		},
	},
}

// kindFieldEnums are allowed-value sets that depend on the kind (the flat enumSources map
// can't express these because the field is shared across kinds with different meanings).
var kindFieldEnums = map[string][]string{
	"pages.provider": {"cloudflare", "github"},
}

// renderKindBlocks emits the per-kind annotated YAML for a union section.
func renderKindBlocks(sectionKey string, kb kindBlock) string {
	byKey := fieldsByYAMLKey(kb.typ)
	var b strings.Builder
	for _, ks := range kb.kinds {
		var lines []string
		for _, key := range ks.fields {
			if f, ok := byKey[key]; ok {
				lines = append(lines, yamlFieldLines(f, kb.typ.Name(), "    ", sectionKey, ks.name)...)
			}
		}
		if len(lines) == 0 {
			continue
		}
		lines[0] = "  - " + strings.TrimLeft(lines[0], " ") // first field becomes the list item
		b.WriteString("#### `kind: " + ks.name + "`\n\n")
		b.WriteString("```yaml\n" + sectionKey + ":\n" + strings.Join(lines, "\n") + "\n```\n\n")
	}
	return b.String()
}

// renderSectionYAML emits a single nested annotated YAML block for a first-party struct
// section (a list of the struct if isList, else a plain nested mapping).
func renderSectionYAML(sectionKey string, t reflect.Type, isList bool) string {
	indent := "  "
	if isList {
		indent = "    " // list-item continuation
	}
	var lines []string
	for i := 0; i < t.NumField(); i++ {
		lines = append(lines, yamlFieldLines(t.Field(i), t.Name(), indent, sectionKey, "")...)
	}
	if len(lines) == 0 {
		return ""
	}
	if isList {
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
	if isFirstPartyConfig(elem) {
		list := unwrapPtr(field.Type).Kind() == reflect.Slice
		childIndent := indent + "  "
		if list {
			childIndent = indent + "    " // room for the "- " list marker
		}
		var children []string
		for i := 0; i < elem.NumField(); i++ {
			children = append(children, yamlFieldLines(elem.Field(i), elem.Name(), childIndent, docPath, "")...)
		}
		head := indent + yamlKey + ":"
		if len(children) == 0 {
			head += " {}" // no documentable children — avoid a dangling "key:"
		}
		if comment != "" {
			head += "   # " + comment
		}
		if list && len(children) > 0 {
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
