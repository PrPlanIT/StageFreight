// Package layout provides terminal-aware text layout primitives.
// It owns: visual width measurement, ANSI-transparent wrapping, and value
// column detection. It has no I/O and no terminal detection — those are the
// caller's responsibility.
package layout

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

const (
	// FramePrefix is the visible column width of the section row prefix "    │ ".
	// Used by callers computing a content budget from terminal width.
	FramePrefix = 6

	// DefaultContentWidth is the fallback content budget when terminal width
	// cannot be detected (CI pipes, file output, etc.).
	// 120 is a safe width for CI log viewers and wide terminals.
	DefaultContentWidth = 120
)

// WrapContent wraps line at word boundaries so each segment fits within budget
// visible columns. ANSI escape sequences are transparent (zero visual width);
// emoji and other wide characters are measured at their true terminal width.
//
// Wrap pattern:
//
//	first line:        "key   value value value"
//	continuation:      "      value value value"
//
// Continuation lines are indented to the value column — the position after the
// first padding gap (2+ spaces) that follows key content.
//
// The "..." ellipsis is used ONLY for hard mid-token cuts (no word boundary
// available). Word-boundary wraps are clean — no decoration.
func WrapContent(line string, budget int) []string {
	if budget < 8 {
		budget = 8 // sane floor; below this, wrapping is meaningless
	}
	if VisualWidth(line) <= budget {
		return []string{line}
	}

	indent := DetectValueIndent(line)
	// A hanging indent must never starve the continuation budget. If aligning to
	// the value column would leave less than half the width for content, abandon
	// the hang — a readable hard-wrap beats one character per line. (The failure
	// mode this guards: a huge, near-spaceless line — a whole `RUN` command dumped
	// into an error box — whose false "value column" collapsed the cut budget to
	// ~1, exploding into hundreds of "<indent> X..." rows.)
	if indent > budget/2 {
		indent = 0
	}
	indentStr := strings.Repeat(" ", indent)

	// A single logical line may not explode into unbounded rows: past this it is
	// truncated with an ellipsis. Bounds the blast radius of any pathological input.
	const maxLines = 20

	var result []string
	remaining := []rune(line)
	first := true

	for len(remaining) > 0 {
		prefix := ""
		cutBudget := budget
		if !first {
			prefix = indentStr
			cutBudget = budget - indent
		}

		// Whole remainder fits — emit and stop.
		if runeSliceWidth(remaining) <= cutBudget {
			result = append(result, prefix+string(remaining))
			break
		}

		// Final allowed row: truncate the remainder rather than wrap further.
		if len(result) >= maxLines-1 {
			cut := findHardCut(remaining, cutBudget-1) // reserve 1 for the ellipsis
			result = append(result, prefix+string(remaining[:cut])+"…")
			break
		}

		if cut := findWordBoundary(remaining, cutBudget); cut >= 0 {
			// Clean word-boundary cut — no decoration.
			result = append(result, prefix+string(remaining[:cut]))
			remaining = []rune(strings.TrimLeft(string(remaining[cut:]), " "))
		} else {
			// No word boundary — hard cut with ellipsis (budget guaranteed ≥ 4 by the
			// indent clamp above, so this always makes real progress).
			hardCut := findHardCut(remaining, cutBudget-3) // reserve 3 for "..."
			result = append(result, prefix+string(remaining[:hardCut])+"...")
			remaining = remaining[hardCut:]
		}
		first = false
	}

	return result
}

// VisualWidth returns the visible column width of s.
// ANSI escape sequences contribute zero width; wide characters (emoji, CJK)
// are counted at their actual terminal column width via go-runewidth.
func VisualWidth(s string) int {
	return runeSliceWidth([]rune(s))
}

// DetectValueIndent returns the column position where the value starts in a
// formatted row string — immediately after the first padding gap of 2+ spaces
// that follows non-space content. Used to align continuation lines.
//
// For "key             value..." this returns the position of 'v' (e.g. 16).
// Returns 0 if no gap is found.
func DetectValueIndent(line string) int {
	runes := stripANSI([]rune(line))
	inContent := false
	spaceStart := -1
	for i, r := range runes {
		if r != ' ' {
			if inContent && spaceStart >= 0 && i-spaceStart >= 2 {
				return runewidth.StringWidth(string(runes[:i]))
			}
			spaceStart = -1
			inContent = true
		} else if inContent && spaceStart < 0 {
			spaceStart = i
		}
	}
	return 0
}

// findWordBoundary returns the rune index of the last space at or before
// maxVisual visible columns, skipping ANSI escape sequences.
// Returns -1 if no word boundary exists within the budget.
func findWordBoundary(runes []rune, maxVisual int) int {
	visual := 0
	lastSpace := -1
	i := 0
	for i < len(runes) {
		if isANSIStart(runes, i) {
			i = skipANSI(runes, i)
			continue
		}
		w := runewidth.RuneWidth(runes[i])
		if visual+w > maxVisual {
			break
		}
		if runes[i] == ' ' {
			lastSpace = i
		}
		visual += w
		i++
	}
	return lastSpace
}

// findHardCut returns the rune index at which to hard-cut so the piece fits
// within maxVisual columns. Used only when no word boundary exists.
func findHardCut(runes []rune, maxVisual int) int {
	visual := 0
	i := 0
	for i < len(runes) {
		if isANSIStart(runes, i) {
			i = skipANSI(runes, i)
			continue
		}
		w := runewidth.RuneWidth(runes[i])
		if visual+w > maxVisual {
			break
		}
		visual += w
		i++
	}
	if i == 0 {
		return 1 // always advance at least one rune
	}
	return i
}

func runeSliceWidth(runes []rune) int {
	width := 0
	i := 0
	for i < len(runes) {
		if isANSIStart(runes, i) {
			i = skipANSI(runes, i)
			continue
		}
		width += runewidth.RuneWidth(runes[i])
		i++
	}
	return width
}

// stripANSI removes ANSI escape sequences from a rune slice.
func stripANSI(runes []rune) []rune {
	var out []rune
	i := 0
	for i < len(runes) {
		if isANSIStart(runes, i) {
			i = skipANSI(runes, i)
			continue
		}
		out = append(out, runes[i])
		i++
	}
	return out
}

func isANSIStart(runes []rune, i int) bool {
	return runes[i] == '\033' && i+1 < len(runes) && runes[i+1] == '['
}

func skipANSI(runes []rune, i int) int {
	i += 2 // skip ESC [
	for i < len(runes) && runes[i] != 'm' {
		i++
	}
	if i < len(runes) {
		i++ // skip 'm'
	}
	return i
}
