package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderTable renders an array of objects as a markdown table.
// columns selects which fields to include. If empty, uses all keys from the first item.
func RenderTable(data interface{}, columns []string) (string, error) {
	items, err := toMapSlice(data)
	if err != nil {
		return "", fmt.Errorf("table renderer: %w", err)
	}
	if len(items) == 0 {
		return "*No items*", nil
	}

	// Determine columns
	if len(columns) == 0 {
		// Collect all keys from first item
		for k := range items[0] {
			columns = append(columns, k)
		}
		// Sort for determinism
		sortStrings(columns)
	}

	// Build header
	var header strings.Builder
	header.WriteString("| ")
	for i, col := range columns {
		if i > 0 {
			header.WriteString(" | ")
		}
		header.WriteString(formatHeader(col))
	}
	header.WriteString(" |")

	// Build separator
	var sep strings.Builder
	sep.WriteString("| ")
	for i := range columns {
		if i > 0 {
			sep.WriteString(" | ")
		}
		sep.WriteString("---")
	}
	sep.WriteString(" |")

	// Build rows
	var rows []string
	rows = append(rows, header.String())
	rows = append(rows, sep.String())

	for _, item := range items {
		var row strings.Builder
		row.WriteString("| ")
		for i, col := range columns {
			if i > 0 {
				row.WriteString(" | ")
			}
			val := formatCell(item[col])
			row.WriteString(val)
		}
		row.WriteString(" |")
		rows = append(rows, row.String())
	}

	return strings.Join(rows, "\n"), nil
}

// RenderList renders an array as a bullet list.
// Items with "name" and "version" fields are rendered as "name: version".
// Otherwise just the "name" or string value.
func RenderList(data interface{}) (string, error) {
	items, err := toMapSlice(data)
	if err != nil {
		// Try as string array
		strs, serr := toStringSlice(data)
		if serr != nil {
			return "", fmt.Errorf("list renderer: %w", err)
		}
		var lines []string
		for _, s := range strs {
			lines = append(lines, "- "+s)
		}
		return strings.Join(lines, "\n"), nil
	}

	if len(items) == 0 {
		return "*No items*", nil
	}

	var lines []string
	for _, item := range items {
		name := formatCell(item["name"])
		version := formatCell(item["version"])
		if version != "" && version != "null" {
			lines = append(lines, fmt.Sprintf("- %s: %s", name, version))
		} else {
			lines = append(lines, "- "+name)
		}
	}

	return strings.Join(lines, "\n"), nil
}

// RenderKV renders a single object as a key-value markdown table.
func RenderKV(data interface{}) (string, error) {
	m, err := toMap(data)
	if err != nil {
		return "", fmt.Errorf("kv renderer: %w", err)
	}
	if len(m) == 0 {
		return "*No data*", nil
	}

	// Sort keys for determinism
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)

	var rows []string
	rows = append(rows, "| Key | Value |")
	rows = append(rows, "| --- | --- |")

	for _, k := range keys {
		rows = append(rows, fmt.Sprintf("| %s | %s |", formatHeader(k), formatCell(m[k])))
	}

	return strings.Join(rows, "\n"), nil
}

// ExtractSection extracts a nested value from a manifest using a dot-path.
// e.g., "inventories.pip" → manifest.Inventories.Pip
func ExtractSection(m *Manifest, dotPath string) (interface{}, error) {
	// Marshal to JSON, then navigate by dot-path
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	parts := strings.Split(dotPath, ".")
	var current interface{} = raw

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("section %q not found at %q", dotPath, part)
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot traverse into %q (not an object)", part)
		}
	}

	return current, nil
}

// RenderSection renders a manifest section using the specified renderer.
func RenderSection(m *Manifest, section, renderer string, columns []string) (string, error) {
	data, err := ExtractSection(m, section)
	if err != nil {
		return "", err
	}

	switch renderer {
	case "table":
		return RenderTable(data, columns)
	case "list":
		return RenderList(data)
	case "kv":
		return RenderKV(data)
	case "badges":
		return RenderBadges(data)
	case "versions":
		return RenderVersions(data)
	default:
		return "", fmt.Errorf("unknown renderer %q", renderer)
	}
}

// RenderBadges renders an array of inventory items as inline shields.io badge images.
// Items with version+pinned get green badges, version+unpinned get blue, no version gets grey.
func RenderBadges(data interface{}) (string, error) {
	items, err := toMapSlice(data)
	if err != nil {
		return "", fmt.Errorf("badges renderer: %w", err)
	}
	if len(items) == 0 {
		return "*No items*", nil
	}

	var badges []string
	for _, item := range items {
		name := formatCell(item["name"])
		version := formatCell(item["version"])
		pinned := false
		if p, ok := item["pinned"]; ok {
			switch v := p.(type) {
			case bool:
				pinned = v
			case string:
				pinned = v == "yes" || v == "true"
			}
		}

		encoded := shieldsEncode(name)
		var badge string
		if version != "" && version != "null" {
			encodedVer := shieldsEncode(version)
			color := "0078D4" // blue = unpinned
			if pinned {
				color = "2ea043" // green = pinned
			}
			badge = fmt.Sprintf("![%s](https://img.shields.io/badge/%s-%s-%s?style=flat)", name, encoded, encodedVer, color)
		} else {
			badge = fmt.Sprintf("![%s](https://img.shields.io/badge/%s-555?style=flat)", name, encoded)
		}
		badges = append(badges, badge)
	}

	return strings.Join(badges, " "), nil
}

// shieldsEncode encodes a string for use in shields.io badge URLs.
// Spaces become underscores, hyphens become double-hyphens, and special chars are URL-encoded.
func shieldsEncode(s string) string {
	s = strings.ReplaceAll(s, "-", "--")
	s = strings.ReplaceAll(s, "_", "__")
	s = strings.ReplaceAll(s, " ", "_")
	// URL-encode characters that break shields.io URLs
	for _, c := range []string{"#", "%", "&", "?", "/"} {
		s = strings.ReplaceAll(s, c, fmt.Sprintf("%%%02X", c[0]))
	}
	return s
}

// RenderVersions renders base image inventory items as a clean version table
// with a "Built from" header line. Source refs are shown only in the header,
// not as a table column — they are parser evidence, not user documentation.
func RenderVersions(data interface{}) (string, error) {
	items, err := toMapSlice(data)
	if err != nil {
		return "", fmt.Errorf("versions renderer: %w", err)
	}
	if len(items) == 0 {
		return "No items", nil
	}

	var b strings.Builder

	// Collect unique source_ref values, preserving first-occurrence order.
	var refs []string
	seen := make(map[string]bool)
	for _, item := range items {
		ref := formatCell(item["source_ref"])
		if ref == "" {
			continue
		}
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	// Render "Built from" header if any refs exist.
	if len(refs) > 0 {
		b.WriteString("Built from ")
		for i, ref := range refs {
			// Strip a leading `FROM ` prefix, and a trailing `@sha256:…` digest pin
			// so a digest-pinned base renders as `alpine:3.23.5`, not the raw digest.
			display := strings.TrimPrefix(ref, "FROM ")
			if at := strings.Index(display, "@sha256:"); at >= 0 {
				display = display[:at]
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("`")
			b.WriteString(display)
			b.WriteString("`")
		}
		b.WriteString(".\n\n")
	}

	// Detect whether any item has a non-empty stage field.
	hasStage := false
	for _, item := range items {
		if s := formatCell(item["stage"]); s != "" {
			hasStage = true
			break
		}
	}

	// Build table header.
	if hasStage {
		b.WriteString("| Name | Version | Stage |\n")
		b.WriteString("| --- | --- | --- |\n")
	} else {
		b.WriteString("| Name | Version |\n")
		b.WriteString("| --- | --- |\n")
	}

	// Build table rows.
	for _, item := range items {
		name := formatCell(item["name"])
		version := formatCell(item["version"])
		if hasStage {
			stage := formatCell(item["stage"])
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", name, version, stage))
		} else {
			b.WriteString(fmt.Sprintf("| %s | %s |\n", name, version))
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func toMapSlice(data interface{}) ([]map[string]interface{}, error) {
	arr, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", data)
	}

	var result []map[string]interface{}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("expected object in array, got %T", item)
		}
		result = append(result, m)
	}
	return result, nil
}

func toStringSlice(data interface{}) ([]string, error) {
	arr, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", data)
	}

	var result []string
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("expected string in array, got %T", item)
		}
		result = append(result, s)
	}
	return result, nil
}

func toMap(data interface{}) (map[string]interface{}, error) {
	m, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected object, got %T", data)
	}
	return m, nil
}

func formatHeader(s string) string {
	// Convert snake_case to Title Case
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func formatCell(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "yes"
		}
		return "no"
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
