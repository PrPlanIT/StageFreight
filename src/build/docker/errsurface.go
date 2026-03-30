package docker

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/output"
)

const maxErrorEntries = 15
const contextTailLines = 5

// BuildErrorEntry is a structured, parsed build error.
// Parsed once, rendered from metadata — no string guessing at render time.
type BuildErrorEntry struct {
	File    string // source file (e.g. "src/k8s/discovery.go")
	Line    int    // line number (0 if unknown)
	Col     int    // column (0 if unknown)
	Tool    string // go, docker, process, generic
	Message string // the error message
	Raw     string // original unparsed line (for fallback rendering)
}

// ParseBuildErrors extracts structured error entries from Docker build stderr.
// Early exit once maxErrorEntries found — Docker builds can hit 1k+ lines.
func ParseBuildErrors(stderr string) ([]BuildErrorEntry, []string) {
	errText := strings.TrimSpace(stderr)
	if errText == "" {
		return nil, nil
	}

	lines := strings.Split(errText, "\n")
	var entries []BuildErrorEntry

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if entry, ok := parseErrorLine(trimmed); ok {
			entries = append(entries, entry)
			if len(entries) >= maxErrorEntries {
				break
			}
		}
	}

	return entries, lines
}

// RenderBuildError extracts and renders structured error entries from Docker build stderr.
// Shared between execute.go and crucible.go — one failure rendering contract.
//
// Three-layer guarantee:
//   1. Structured parse (best) — file, line, tool, message
//   2. Generic parse (ok) — error line with partial metadata
//   3. Raw fallback (always works) — last N lines, never empty
//
// The fallback MUST NEVER be removed. It is the guarantee that errors are always visible.
func RenderBuildError(sec *output.Section, stderr string) {
	entries, lines := ParseBuildErrors(stderr)
	if len(lines) == 0 {
		return
	}

	if len(entries) > 0 {
		for _, e := range entries {
			sec.Row("  %s", formatError(e))
		}

		// Context tail — which Docker stage/step failed.
		sec.Row("")
		start := 0
		if len(lines) > contextTailLines {
			start = len(lines) - contextTailLines
		}
		for _, line := range lines[start:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			sec.Row("  %s", trimmed)
		}
	} else {
		// Fallback: no recognized error pattern. Show last 10 lines.
		// This layer MUST ALWAYS exist — it is the safety net for unknown build tools.
		start := 0
		if len(lines) > 10 {
			start = len(lines) - 10
			sec.Row("... (%d lines truncated)", start)
		}
		for _, line := range lines[start:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			sec.Row("  %s", trimmed)
		}
	}
}

// formatError renders a structured error entry as a human-readable line.
func formatError(e BuildErrorEntry) string {
	if e.File != "" && e.Line > 0 {
		loc := e.File + ":" + strconv.Itoa(e.Line)
		if e.Col > 0 {
			loc += ":" + strconv.Itoa(e.Col)
		}
		return fmt.Sprintf("[%s] %s: %s", e.Tool, loc, e.Message)
	}
	if e.Tool != "" && e.Tool != "generic" {
		return fmt.Sprintf("[%s] %s", e.Tool, e.Message)
	}
	return e.Raw
}

// parseErrorLine attempts to parse a single line into a structured BuildErrorEntry.
func parseErrorLine(line string) (BuildErrorEntry, bool) {
	lower := strings.ToLower(line)

	// Go compile error: "src/k8s/discovery.go:33:80: undefined: config"
	if entry, ok := parseGoError(line); ok {
		return entry, true
	}

	// Docker ERROR lines: "ERROR: failed to build: ..."
	if strings.HasPrefix(line, "ERROR:") || strings.HasPrefix(line, "error:") {
		msg := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "ERROR:"), "error:"))
		return BuildErrorEntry{
			Tool:    "docker",
			Message: msg,
			Raw:     line,
		}, true
	}

	// Process failure
	if strings.Contains(lower, "did not complete successfully") {
		return BuildErrorEntry{
			Tool:    "docker",
			Message: line,
			Raw:     line,
		}, true
	}

	// Exit code
	if strings.Contains(lower, "exit code:") || strings.Contains(lower, "exit status") {
		return BuildErrorEntry{
			Tool:    "process",
			Message: line,
			Raw:     line,
		}, true
	}

	// Generic compiler-style — filter Docker progress noise
	if strings.Contains(line, ":") &&
		(strings.Contains(lower, "error") || strings.Contains(lower, "undefined") || strings.Contains(lower, "failed")) &&
		!strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "---") && !strings.Contains(line, " => ") {

		entry := BuildErrorEntry{Tool: "generic", Message: line, Raw: line}
		if parts := strings.SplitN(line, ":", 3); len(parts) >= 3 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				entry.File = strings.TrimSpace(parts[0])
				entry.Line = n
				entry.Message = strings.TrimSpace(parts[2])
			}
		}
		return entry, true
	}

	return BuildErrorEntry{}, false
}

// parseGoError parses a Go compiler error line into structured metadata.
func parseGoError(line string) (BuildErrorEntry, bool) {
	goIdx := strings.Index(line, ".go:")
	if goIdx < 0 {
		return BuildErrorEntry{}, false
	}

	lower := strings.ToLower(line)
	isGoError := strings.Contains(lower, "undefined") ||
		strings.Contains(lower, "cannot") ||
		strings.Contains(lower, "imported and not used") ||
		strings.Contains(lower, "too many errors") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "declared and not used") ||
		strings.Contains(lower, "not enough arguments") ||
		strings.Contains(lower, "too many arguments") ||
		strings.Contains(lower, "missing return") ||
		strings.Contains(lower, "type mismatch") ||
		strings.Contains(lower, "redeclared")

	if !isGoError {
		return BuildErrorEntry{}, false
	}

	file := line[:goIdx+3]
	rest := line[goIdx+4:]

	lineNum := 0
	col := 0
	msg := rest

	parts := strings.SplitN(rest, ":", 3)
	if len(parts) >= 1 {
		if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
			lineNum = n
			msg = ""
			if len(parts) >= 2 {
				if c, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
					col = c
					if len(parts) >= 3 {
						msg = strings.TrimSpace(parts[2])
					}
				} else {
					msg = strings.TrimSpace(strings.Join(parts[1:], ":"))
				}
			}
		}
	}

	return BuildErrorEntry{
		File:    file,
		Line:    lineNum,
		Col:     col,
		Tool:    "go",
		Message: msg,
		Raw:     line,
	}, true
}
