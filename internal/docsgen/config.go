package docsgen

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// GenerateConfigReference emits a complete config reference markdown document.
// Fields are discovered via reflection on the config structs. The override maps
// provide curated descriptions, allowed values, defaults, and notes.
func GenerateConfigReference() string {
	var b strings.Builder
	b.WriteString(generatedHeader)

	sections := discoverSections()

	// Table of contents.
	b.WriteString("## Config Sections\n\n")
	for _, s := range sections {
		b.WriteString(fmt.Sprintf("- [`%s`](#%s)\n", s.Key, anchor("config", s.Key)))
	}
	b.WriteString("\n---\n\n")

	// Per-section documentation.
	for _, s := range sections {
		b.WriteString(renderConfigSection(s))
	}

	return b.String()
}

// configSection represents a top-level config key and its fields.
type configSection struct {
	Key    string
	Fields []fieldRow
}

func renderConfigSection(s configSection) string {
	var b strings.Builder

	b.WriteString(anchorTag("config", s.Key) + "\n")
	b.WriteString(fmt.Sprintf("### %s\n\n", s.Key))

	// Section summary from overrides.
	if so, ok := sectionOverrides[s.Key]; ok && so.Summary != "" {
		b.WriteString(so.Summary + "\n\n")
	}

	// Field table.
	if len(s.Fields) > 0 {
		b.WriteString(fieldTable(s.Fields))
		b.WriteString("\n")
	}

	// Per-field allowed values and notes.
	for _, f := range s.Fields {
		fo := getFieldOverride(s.Key + "." + f.YAMLKey)
		if len(fo.AllowedValues) > 0 {
			quoted := make([]string, len(fo.AllowedValues))
			for i, v := range fo.AllowedValues {
				quoted[i] = "`" + v + "`"
			}
			b.WriteString(fmt.Sprintf("**`%s` allowed values:** %s\n\n", f.YAMLKey, strings.Join(quoted, ", ")))
		}
		if len(fo.Notes) > 0 {
			for _, n := range fo.Notes {
				b.WriteString(fmt.Sprintf("> %s\n", n))
			}
			b.WriteString("\n")
		}
	}

	// Section-level notes and example.
	if so, ok := sectionOverrides[s.Key]; ok {
		if len(so.Notes) > 0 {
			for _, n := range so.Notes {
				b.WriteString(fmt.Sprintf("> %s\n", n))
			}
			b.WriteString("\n")
		}
		if so.Example != "" {
			b.WriteString("**Example:**\n\n```yaml\n" + so.Example + "\n```\n\n")
		}
	}

	b.WriteString("---\n\n")
	return b.String()
}

func getFieldOverride(key string) FieldOverride {
	if fo, ok := fieldOverrides[key]; ok {
		return fo
	}
	return FieldOverride{}
}

// isFirstPartyConfig returns true if t is a struct defined in the stagefreight config package.
func isFirstPartyConfig(t reflect.Type) bool {
	return t.Kind() == reflect.Struct &&
		t.PkgPath() == "github.com/PrPlanIT/StageFreight/src/config"
}

// discoverSections walks the config.Config struct via reflection to discover
// all top-level sections and their fields. Ordering follows struct field order.
func discoverSections() []configSection {
	t := reflect.TypeOf(config.Config{})
	var sections []configSection

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		yamlKey := yamlKeyFromTag(field.Tag.Get("yaml"))
		if yamlKey == "" || yamlKey == "-" {
			continue
		}

		section := configSection{Key: yamlKey}

		// Unwrap pointer, then slice/map to find element type.
		elemType := field.Type
		if elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
		}
		switch elemType.Kind() {
		case reflect.Slice:
			elemType = elemType.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
		case reflect.Map:
			// map fields are a single entry
			section.Fields = []fieldRow{reflectField(field, yamlKey)}
			sections = append(sections, section)
			continue
		}

		if elemType.Kind() == reflect.Struct {
			section.Fields = walkStruct(elemType, yamlKey)
		} else {
			section.Fields = []fieldRow{reflectField(field, yamlKey)}
		}

		sections = append(sections, section)
	}

	return sections
}

// walkStruct recursively discovers fields from a struct type.
// prefix is the docs-path prefix (e.g., "builds" or "targets.when").
func walkStruct(t reflect.Type, prefix string) []fieldRow {
	// Skip third-party structs (e.g., yaml.Node used for Defaults).
	if !isFirstPartyConfig(t) {
		return nil
	}

	var rows []fieldRow
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Handle embedded structs (inline yaml tags).
		tag := field.Tag.Get("yaml")
		if tag == ",inline" {
			rows = append(rows, walkStruct(field.Type, prefix)...)
			continue
		}

		yamlKey := yamlKeyFromTag(tag)
		if yamlKey == "" || yamlKey == "-" {
			continue
		}

		// Unwrap pointer/slice to find element type.
		elemType := field.Type
		if elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
		}
		if elemType.Kind() == reflect.Slice {
			elemType = elemType.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
		}

		// Recurse into first-party config structs to flatten nested fields.
		if isFirstPartyConfig(elemType) {
			nestedPrefix := prefix + "." + yamlKey
			rows = append(rows, walkStruct(elemType, nestedPrefix)...)
		} else {
			rows = append(rows, reflectField(field, prefix))
		}
	}

	return rows
}

// reflectField builds a fieldRow from a reflected struct field, enriched by overrides.
// prefix is the full docs-path prefix (e.g., "builds" or "targets.when").
func reflectField(field reflect.StructField, prefix string) fieldRow {
	yamlKey := yamlKeyFromTag(field.Tag.Get("yaml"))
	docPath := prefix + "." + yamlKey
	fo := getFieldOverride(docPath)

	// Determine type string.
	typeName := goTypeString(field.Type)

	// Determine required: fields without omitempty in yaml tag are required.
	required := !strings.Contains(field.Tag.Get("yaml"), "omitempty")
	if fo.Required != nil {
		required = *fo.Required
	}

	// Description: override > doc comment > type fallback.
	desc := fo.Description
	if desc == "" {
		desc = typeDescription(field.Type)
	}

	// relKey is the section-relative key: strip the first dot-segment (section name).
	// e.g., "targets.when.git_tags" → "when.git_tags", "builds.id" → "id"
	relKey := docPath[strings.Index(docPath, ".")+1:]

	return fieldRow{
		Name:        yamlKey,
		YAMLKey:     relKey,
		Type:        typeName,
		Required:    required,
		Default:     fo.Default,
		Description: desc,
	}
}

// yamlKeyFromTag extracts the YAML key from a struct tag like "field_name,omitempty".
func yamlKeyFromTag(tag string) string {
	if tag == "" {
		return ""
	}
	parts := strings.SplitN(tag, ",", 2)
	return parts[0]
}

// goTypeString returns a human-readable type string for documentation.
func goTypeString(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Ptr:
		return goTypeString(t.Elem())
	case reflect.Slice:
		return "[]" + goTypeString(t.Elem())
	case reflect.Map:
		return "map[" + goTypeString(t.Key()) + "]" + goTypeString(t.Elem())
	case reflect.Struct:
		// For well-known types, use friendly names.
		name := t.Name()
		if name != "" {
			switch name {
			case "Node":
				return "object"
			default:
				return "object"
			}
		}
		return "object"
	case reflect.Interface:
		return "any"
	default:
		return t.Kind().String()
	}
}

// typeDescription returns a fallback description derived from the type.
func typeDescription(t reflect.Type) string {
	return goTypeString(t) + " value"
}
