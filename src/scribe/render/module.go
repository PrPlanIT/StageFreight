// Package render composes scribe modules into managed README sections.
//
// Modules are pluggable content producers (badges, shields, text, etc.)
// that render to inline markdown. The composer orchestrates them into rows
// with layout control, then the result gets injected into <!-- sf:<name> -->
// sections via the section primitives.
//
// Items within a row are space-joined (inline).
// Rows are blank-line-separated (markdown paragraph breaks).
// Break modules force a new row.
package render

import "strings"

// Module produces inline markdown content for a single item.
type Module interface {
	Render() string
}

// BreakModule forces a line break in composition.
// Items before the break are on one line, items after start a new line.
type BreakModule struct{}

// Render returns empty — breaks are handled by Compose, not rendered.
func (BreakModule) Render() string { return "" }

// Compose takes a flat list of modules. Items are space-joined until a
// BreakModule forces a new row. Rows are separated by blank lines for
// proper markdown paragraph breaks. Returns the composed content string.
func Compose(modules []Module) string {
	var lines []string
	var current []string

	for _, m := range modules {
		if _, isBreak := m.(BreakModule); isBreak {
			if len(current) > 0 {
				lines = append(lines, strings.Join(current, " "))
				current = nil
			}
			continue
		}
		if s := m.Render(); s != "" {
			current = append(current, s)
		}
	}
	if len(current) > 0 {
		lines = append(lines, strings.Join(current, " "))
	}
	return strings.Join(lines, "\n\n")
}

// ComposeInline renders all modules and joins them with spaces on a single line.
// BreakModules are ignored. Use for inline badge rows.
func ComposeInline(modules []Module) string {
	var parts []string
	for _, m := range modules {
		if _, isBreak := m.(BreakModule); isBreak {
			continue
		}
		if s := m.Render(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// ComposeRows takes rows of modules (pre-grouped). Items within a row are
// space-joined. Rows are newline-joined. Kept for backward compatibility.
func ComposeRows(rows [][]Module) string {
	var lines []string
	for _, row := range rows {
		var parts []string
		for _, m := range row {
			if s := m.Render(); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, " "))
		}
	}
	return strings.Join(lines, "\n")
}
