package output

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/sofmeright/stagefreight/src/lint"
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

		for _, f := range ff {
			var loc string
			switch {
			case f.Line == 0:
				loc = "-"
			case f.Column > 0:
				loc = fmt.Sprintf("%d:%d", f.Line, f.Column)
			default:
				loc = fmt.Sprintf("%d", f.Line)
			}
			sev := severityTag(f.Severity, color)
			sec.Row("  %-8s %-4s  %-10s %s", loc, sev, f.Module, f.Message)
		}

		sec.Row("")
	}
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
