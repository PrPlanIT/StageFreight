package output

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// Colors for terminal output.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// Printer formats and writes lint findings.
type Printer struct {
	Writer io.Writer
	Color  bool
}

// NewPrinter creates a printer writing to stdout with color auto-detection.
func NewPrinter() *Printer {
	return &Printer{
		Writer: os.Stdout,
		Color:  isTerminal(),
	}
}

// Print outputs findings grouped by file, returns true if any critical findings exist.
func (p *Printer) Print(findings []lint.Finding) bool {
	if len(findings) == 0 {
		return false
	}

	// Group by file
	grouped := make(map[string][]lint.Finding)
	for _, f := range findings {
		grouped[f.File] = append(grouped[f.File], f)
	}

	// Sort files
	files := make([]string, 0, len(grouped))
	for f := range grouped {
		files = append(files, f)
	}
	sort.Strings(files)

	hasCritical := false

	for _, file := range files {
		ff := grouped[file]

		// Sort by line number within file
		sort.Slice(ff, func(i, j int) bool {
			if ff[i].Line != ff[j].Line {
				return ff[i].Line < ff[j].Line
			}
			return ff[i].Column < ff[j].Column
		})

		fmt.Fprintf(p.Writer, "\n%s\n", p.colorize(file, colorBold))

		for _, f := range ff {
			sev := p.severityStr(f.Severity)
			if f.Severity == lint.SeverityCritical {
				hasCritical = true
			}

			loc := fmt.Sprintf("%d", f.Line)
			if f.Column > 0 {
				loc = fmt.Sprintf("%d:%d", f.Line, f.Column)
			}

			fmt.Fprintf(p.Writer, "  %s %s %s %s\n",
				p.colorize(loc, colorGray),
				sev,
				p.colorize(f.Module, colorCyan),
				f.Message,
			)
		}
	}

	return hasCritical
}

// Summary prints a final summary line.
func (p *Printer) Summary(total, critical, warning, info int, filesScanned int) {
	fmt.Fprintf(p.Writer, "\n%s\n", FindingsSummaryLine(total, critical, warning, info, filesScanned, p.Color))
}

// FindingsSummaryLine returns a one-line findings summary, optionally colored.
func FindingsSummaryLine(total, critical, warning, info, filesScanned int, color bool) string {
	parts := []string{}
	if critical > 0 {
		s := fmt.Sprintf("%d critical", critical)
		if color {
			s = colorRed + s + colorReset
		}
		parts = append(parts, s)
	}
	if warning > 0 {
		s := fmt.Sprintf("%d warning", warning)
		if color {
			s = colorYellow + s + colorReset
		}
		parts = append(parts, s)
	}
	if info > 0 {
		parts = append(parts, fmt.Sprintf("%d info", info))
	}

	summary := "no findings"
	if len(parts) > 0 {
		summary = strings.Join(parts, ", ")
	}

	totalStr := fmt.Sprintf("%d", total)
	if color {
		totalStr = colorBold + totalStr + colorReset
	}
	return fmt.Sprintf("%s findings in %d files: %s", totalStr, filesScanned, summary)
}

func (p *Printer) severityStr(s lint.Severity) string {
	return severityTag(s, p.Color)
}

// severityTag returns a short severity label, optionally colored.
func severityTag(s lint.Severity, color bool) string {
	switch s {
	case lint.SeverityCritical:
		if color {
			return colorRed + "CRIT" + colorReset
		}
		return "CRIT"
	case lint.SeverityWarning:
		if color {
			return colorYellow + "WARN" + colorReset
		}
		return "WARN"
	case lint.SeverityInfo:
		if color {
			return colorGray + "INFO" + colorReset
		}
		return "INFO"
	default:
		return s.String()
	}
}

func (p *Printer) colorize(text, color string) string {
	if !p.Color {
		return text
	}
	return color + text + colorReset
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// UseColor returns true if colored output should be used.
// Respects NO_COLOR env, TERM=dumb, and terminal detection.
func UseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal() || IsCI()
}

// LintTable writes a per-module stats table inside a section.
func LintTable(w io.Writer, stats []lint.ModuleStats, _ bool) {
	// Header
	fmt.Fprintf(w, "    │ %-16s%6s  %6s  %s\n", "module", "files", "cached", "findings")

	for _, s := range stats {
		fmt.Fprintf(w, "    │ %-16s%5d   %5d   %5d\n", s.Name, s.Files, s.Cached, s.Findings)
	}
}

// SectionFindings renders findings grouped by file inside a section.
// Files are sorted lexicographically; findings within each file by line, col, module, message.
func SectionFindings(sec *Section, findings []lint.Finding, color bool) {
	if len(findings) == 0 {
		return
	}

	byFile := map[string][]lint.Finding{}
	for _, f := range findings {
		byFile[f.File] = append(byFile[f.File], f)
	}

	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)

	sec.Row("")

	for _, file := range files {
		ff := byFile[file]
		sort.Slice(ff, func(i, j int) bool {
			a, b := ff[i], ff[j]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			if a.Column != b.Column {
				return a.Column < b.Column
			}
			if a.Module != b.Module {
				return a.Module < b.Module
			}
			return a.Message < b.Message
		})

		if color {
			sec.Row("%s", colorBold+file+colorReset)
		} else {
			sec.Row("%s", file)
		}

		// Collapse repeated findings (same module + message + severity) into one row
		// with compressed line ranges — "trailing whitespace ×80  lines 14, 18, 25-29".
		// Volume shrinks; signal does not: this is presentation only. The raw findings,
		// the critical/warning/info counts, and the JSON/JUnit outputs are unchanged.
		type groupKey struct {
			module, message string
			severity        lint.Severity
		}
		groups := map[groupKey][]lint.Finding{}
		order := []groupKey{}
		for _, f := range ff {
			k := groupKey{f.Module, f.Message, f.Severity}
			if _, seen := groups[k]; !seen {
				order = append(order, k)
			}
			groups[k] = append(groups[k], f)
		}
		// ff is already line-sorted, so first-appearance order is line order.

		for _, k := range order {
			g := groups[k]
			sev := severityTag(k.severity, color)
			if len(g) == 1 {
				sec.Row("  %-8s %-4s  %-10s %s", findingLoc(g[0]), sev, k.module, k.message)
				continue
			}
			count := fmt.Sprintf("×%d", len(g))
			if ranges, more := compressLineRanges(g); ranges != "" {
				suffix := ranges
				if more > 0 {
					suffix = fmt.Sprintf("%s …(+%d)", ranges, more)
				}
				sec.Row("  %-8s %-4s  %-10s %s  lines %s", count, sev, k.module, k.message, suffix)
			} else {
				sec.Row("  %-8s %-4s  %-10s %s", count, sev, k.module, k.message)
			}
		}

		sec.Row("")
	}
}

// findingLoc renders a single finding's location: "line", "line:col", or "-".
func findingLoc(f lint.Finding) string {
	switch {
	case f.Line == 0:
		return "-"
	case f.Column > 0:
		return fmt.Sprintf("%d:%d", f.Line, f.Column)
	default:
		return fmt.Sprintf("%d", f.Line)
	}
}

// compressLineRanges turns a group's line numbers into a compact range string
// ("14, 18, 25-29"), capped at maxRangeTokens with the remainder counted in `more`.
// Returns "" when no group member has a positive line number (e.g. file-level findings).
func compressLineRanges(g []lint.Finding) (display string, more int) {
	const maxRangeTokens = 12
	lines := make([]int, 0, len(g))
	for _, f := range g {
		if f.Line > 0 {
			lines = append(lines, f.Line)
		}
	}
	if len(lines) == 0 {
		return "", 0
	}
	sort.Ints(lines)

	var tokens []string
	start, prev := lines[0], lines[0]
	flush := func() {
		if start == prev {
			tokens = append(tokens, fmt.Sprintf("%d", start))
		} else {
			tokens = append(tokens, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, n := range lines[1:] {
		if n == prev { // dedupe (e.g. two findings on the same line)
			continue
		}
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()

	if len(tokens) > maxRangeTokens {
		more = len(tokens) - maxRangeTokens
		tokens = tokens[:maxRangeTokens]
	}
	return strings.Join(tokens, ", "), more
}

// RowStatus writes a row with label, detail, and a status icon.
func RowStatus(sec *Section, label, detail, status string, color bool) {
	icon := StatusIcon(status, color)
	if detail != "" {
		sec.Row("%s %s  %s", label, icon, detail)
	} else {
		sec.Row("%s %s", label, icon)
	}
}
