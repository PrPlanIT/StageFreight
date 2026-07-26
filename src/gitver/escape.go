package gitver

import "strings"

// Template-literal escaping: {{...}} is a literal {...} that must survive template
// resolution AND any "unresolved template" guards (e.g. a badge showing "dev-{sha}"
// as a scheme, not the expanded sha). The caller escapes BEFORE resolving, then
// restores AFTER its own guards run — so a sentinel'd literal is never mistaken for
// an unresolved template. Sentinels are null-delimited so they can't occur in config.
const (
	litOpen  = "\x00sfLB\x00"
	litClose = "\x00sfRB\x00"
)

// EscapeLiterals replaces {{ and }} with internal sentinels so their contents are not
// resolved as templates. Idempotent-ish: an already-escaped string has no {{ / }}.
func EscapeLiterals(s string) string {
	s = strings.ReplaceAll(s, "{{", litOpen)
	s = strings.ReplaceAll(s, "}}", litClose)
	return s
}

// RestoreLiterals turns the sentinels back into literal single braces.
func RestoreLiterals(s string) string {
	s = strings.ReplaceAll(s, litOpen, "{")
	s = strings.ReplaceAll(s, litClose, "}")
	return s
}
