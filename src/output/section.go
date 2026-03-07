package output

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const sectionWidth = 61 // inner width between │ and line end

// Section renders a box-drawing framed output section.
type Section struct {
	w     io.Writer
	name  string
	color bool
}

// NewSection creates a section and writes its header.
// If elapsed is non-zero, it appears right-aligned in the header.
func NewSection(w io.Writer, name string, elapsed time.Duration, color bool) *Section {
	s := &Section{w: w, name: name, color: color}
	s.writeHeader(elapsed)
	return s
}

// Row writes a content line inside the section frame.
func (s *Section) Row(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	fmt.Fprintf(s.w, "    │ %s\n", line)
}

// Separator writes a mid-section divider.
func (s *Section) Separator() {
	fmt.Fprintf(s.w, "    ├%s\n", strings.Repeat("─", sectionWidth))
}

// Close writes the section footer.
func (s *Section) Close() {
	fmt.Fprintf(s.w, "    └%s\n", strings.Repeat("─", sectionWidth))
}

// writeHeader renders: ── Name ──────────────────── elapsed ──
func (s *Section) writeHeader(elapsed time.Duration) {
	label := fmt.Sprintf("── %s ", s.name)

	var suffix string
	if elapsed > 0 {
		suffix = fmt.Sprintf(" %s ──", formatElapsed(elapsed))
	} else {
		suffix = "──"
	}

	fill := sectionWidth + 4 - len(label) - len(suffix)
	if fill < 1 {
		fill = 1
	}

	if s.color {
		// dim cyan for header
		fmt.Fprintf(s.w, "\n    \033[2;36m%s%s%s\033[0m\n", label, strings.Repeat("─", fill), suffix)
	} else {
		fmt.Fprintf(s.w, "\n    %s%s%s\n", label, strings.Repeat("─", fill), suffix)
	}
}

// StatusIcon returns a colored status icon.
func StatusIcon(status string, color bool) string {
	if !color {
		switch status {
		case "success":
			return "✓"
		case "failed":
			return "✗"
		case "warning", "info":
			return "⚠"
		default:
			return "⊘"
		}
	}
	switch status {
	case "success":
		return "\033[32m✓\033[0m"
	case "failed":
		return "\033[31m✗\033[0m"
	case "warning", "info":
		return "\033[33m⚠\033[0m"
	default:
		return "\033[33m⊘\033[0m"
	}
}

// Dimmed returns dimmed text if color is enabled.
func Dimmed(text string, color bool) string {
	if !color {
		return text
	}
	return "\033[90m" + text + "\033[0m"
}

// ContextBlock prints the pipeline context header.
// Replaces the old CIHeader with an aligned key-value layout.
func ContextBlock(w io.Writer, kv []KV) {
	if len(kv) == 0 {
		return
	}
	fmt.Fprintln(w)
	// Print in two-column pairs per line
	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			fmt.Fprintf(w, "    %-12s%-14s%-11s%s\n",
				kv[i].Key, kv[i].Value, kv[i+1].Key, kv[i+1].Value)
		} else {
			fmt.Fprintf(w, "    %-12s%s\n", kv[i].Key, kv[i].Value)
		}
	}
}

// KV is a key-value pair for the context block.
type KV struct {
	Key   string
	Value string
}

// formatElapsed formats a duration for display in section headers.
func formatElapsed(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := d.Seconds() - float64(mins*60)
	return fmt.Sprintf("%dm%.1fs", mins, secs)
}

// SummaryRow writes a summary line with status icon.
func SummaryRow(w io.Writer, name, status, detail string, color bool) {
	icon := StatusIcon(status, color)
	fmt.Fprintf(w, "    │ %-14s%s  %s\n", name, icon, detail)
}

// SummaryTotal writes the final total line.
func SummaryTotal(w io.Writer, elapsed time.Duration, status string, color bool) {
	icon := StatusIcon(status, color)
	fmt.Fprintf(w, "    │ %-14s%s  %s\n", "total", icon, formatElapsed(elapsed))
}
