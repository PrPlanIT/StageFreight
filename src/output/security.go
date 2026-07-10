package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/severity"
)

const (
	SoftBudget         = 15
	HardBudget         = 30
	AbsoluteMax        = 200
	OverwhelmThreshold = 1000

	DefaultOverwhelmLink = "https://www.psychologytoday.com/us/basics/anxiety"
)

var DefaultOverwhelmMessage = []string{"…maybe start here:"}

// VulnRow is the view model for a single vulnerability in CLI output.
type VulnRow struct {
	ID        string // "CVE-2024-45337", "GHSA-xxxx-yyyy"
	Severity  string // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	Package   string // "golang.org/x/crypto"
	Installed string // "0.28.0"
	FixedIn   string // "0.31.0" (empty = no fix)
	Title     string // one-line description
}

// ScanAudit holds metadata for the audit block at the top of the section.
type ScanAudit struct {
	Engine string // "Trivy 0.58.1"
	OS     string // "alpine 3.21.3"
}

// SecurityUX controls the >OverwhelmThreshold message/link.
// Caller is responsible for defaulting and env override behavior.
type SecurityUX struct {
	OverwhelmMessage []string
	OverwhelmLink    string
}

// ScanAuditRows renders the engine/OS audit lines (skips empty fields).
func ScanAuditRows(sec *Section, audit ScanAudit) {
	if audit.Engine != "" {
		sec.Row("%-16s%s", "engine", audit.Engine)
	}
	if audit.OS != "" {
		sec.Row("%-16s%s", "os", audit.OS)
	}
}

// SectionVulns renders the "Vulnerabilities (N)" block with severity-prioritized truncation.
// budget: max rows to display (15 for detailed, 30 for full).
// CRITICAL and HIGH always shown regardless of budget (up to AbsoluteMax).
func SectionVulns(sec *Section, vulns []VulnRow, color bool, budget int, ux SecurityUX) {
	if len(vulns) == 0 {
		return
	}

	sorted := sortVulns(vulns)

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf("Vulnerabilities (%d)", len(sorted))))

	// Overwhelm short-circuit (before budget walk).
	if len(sorted) > OverwhelmThreshold {
		show := 20
		if show > len(sorted) {
			show = len(sorted)
		}
		for i := 0; i < show; i++ {
			renderVulnRow(sec, sorted[i], color)
		}
		remaining := len(sorted) - show

		// Fully disabled — single compact line.
		if len(ux.OverwhelmMessage) == 0 && ux.OverwhelmLink == "" {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more (see security-scan.json)", remaining), color))
			return
		}

		sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more", remaining), color))
		for _, line := range ux.OverwhelmMessage {
			sec.Row("%s", Dimmed("    "+line, color))
		}
		if ux.OverwhelmLink != "" {
			sec.Row("%s", Dimmed("      "+ux.OverwhelmLink, color))
		}
		sec.Row("%s", Dimmed("  (see security-scan.json for the full list)", color))
		return
	}

	// Normal path: severity-prioritized budget walk.
	emitted := 0
	hitAbsMax := false

	for _, v := range sorted {
		if emitted >= AbsoluteMax {
			hitAbsMax = true
			break
		}

		r := severity.Order(v.Severity)
		if r <= 1 {
			// CRIT/HIGH always shown (until AbsoluteMax).
			renderVulnRow(sec, v, color)
			emitted++
			continue
		}

		// MED/LOW/UNK only while under budget.
		if emitted < budget {
			renderVulnRow(sec, v, color)
			emitted++
		}
	}

	if emitted < len(sorted) {
		remaining := len(sorted) - emitted
		if hitAbsMax {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more (hit max output %d; see security-scan.json)", remaining, AbsoluteMax), color))
		} else {
			sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more (see security-scan.json)", remaining), color))
		}
	}
}

// VulnSeverityTag returns a short severity label, optionally colored.
// CRITICAL→"CRIT" red, HIGH→"HIGH" red, MEDIUM/MODERATE→"MOD " yellow,
// LOW→"LOW " gray, UNKNOWN/empty→"UNK " gray.
func VulnSeverityTag(label string, color bool) string {
	sev := severity.Normalize(label)

	tag := "UNK "
	ansi := colorGray

	switch sev {
	case "CRITICAL":
		tag, ansi = "CRIT", colorRed
	case "HIGH":
		tag, ansi = "HIGH", colorRed
	case "MEDIUM":
		tag, ansi = "MOD ", colorYellow
	case "LOW":
		tag, ansi = "LOW ", colorGray
	}

	if !color {
		return tag
	}
	return ansi + tag + colorReset
}

// VulnURL derives an advisory URL from a vulnerability ID.
// GHSA- → github.com/advisories, GO- → pkg.go.dev/vuln, default → osv.dev/vulnerability.
func VulnURL(id string) string {
	id = strings.TrimSpace(id)
	upper := strings.ToUpper(id)

	switch {
	case strings.HasPrefix(upper, "GHSA-"):
		return "https://github.com/advisories/" + id
	case strings.HasPrefix(upper, "GO-"):
		return "https://pkg.go.dev/vuln/" + id
	default:
		return "https://osv.dev/vulnerability/" + id
	}
}

// --- unexported helpers (shared by deps.go via same package) ---

func bold(color bool, s string) string {
	if !color {
		return s
	}
	return colorBold + s + colorReset
}

func sortVulns(vulns []VulnRow) []VulnRow {
	out := make([]VulnRow, len(vulns))
	copy(out, vulns)

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]

		ra, rb := severity.Order(a.Severity), severity.Order(b.Severity)
		if ra != rb {
			return ra < rb // ascending rank = descending severity
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Package < b.Package
	})

	return out
}

func renderVulnRow(sec *Section, v VulnRow, color bool) {
	id := strings.TrimSpace(v.ID)
	pkg := strings.TrimSpace(v.Package)
	tag := VulnSeverityTag(v.Severity, color)

	sec.Row("  %-14s  %-4s  %s", id, tag, pkg)

	installed := strings.TrimSpace(v.Installed)
	fixed := strings.TrimSpace(v.FixedIn)
	title := strings.TrimSpace(v.Title)

	if fixed == "" {
		if title != "" {
			sec.Row("    %s → %s  %s", installed, Dimmed("(no fix)", color), title)
		} else {
			sec.Row("    %s → %s", installed, Dimmed("(no fix)", color))
		}
	} else {
		if title != "" {
			sec.Row("    %s → %s  %s", installed, fixed, title)
		} else {
			sec.Row("    %s → %s", installed, fixed)
		}
	}

	// URL line only for CRIT/HIGH to save vertical space.
	if severity.Order(v.Severity) <= 1 && id != "" {
		sec.Row("%s", Dimmed("    "+VulnURL(id), color))
	}
}
